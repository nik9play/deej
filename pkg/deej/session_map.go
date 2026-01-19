package deej

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/nik9play/deej/pkg/deej/util"
	"github.com/thoas/go-funk"
	"go.uber.org/zap"
)

type sessionMap struct {
	deej   *Deej
	logger *zap.SugaredLogger

	m    map[string][]Session
	lock sync.Locker

	sessionFinder SessionFinder

	lastSessionRefresh time.Time
	unmappedSessions   []Session

	// indicates if we're using event-driven session tracking
	eventDriven bool

	// channel for notifying about session count changes
	sessionCountChangeChan chan struct{}
}

const (
	masterSessionName = "master" // master device volume
	systemSessionName = "system" // system sounds volume
	inputSessionName  = "mic"    // microphone input level

	// some targets need to be transformed before their correct audio sessions can be accessed.
	// this prefix identifies those targets to ensure they don't contradict with another similarly-named process
	specialTargetTransformPrefix = "deej."

	// targets the currently active window (Windows-only, experimental)
	specialTargetCurrentWindow = "current"

	// targets the currently active fullscreen window (Windows-only, experimental)
	specialTargetCurrentFullscreenWindow = "current.fullscreen"

	// targets all currently unmapped sessions (experimental)
	specialTargetAllUnmapped = "unmapped"

	// this threshold constant assumes that re-acquiring all sessions is a kind of expensive operation,
	// and needs to be limited in some manner. this value was previously user-configurable through a config
	// key "process_refresh_frequency", but exposing this type of implementation detail seems wrong now
	minTimeBetweenSessionRefreshes = time.Second * 2
)

// this matches friendly device names (on Windows), e.g. "Headphones (Realtek Audio)"
var deviceSessionKeyPattern = regexp.MustCompile(`^.+ \(.+\)$`)

func newSessionMap(deej *Deej, logger *zap.SugaredLogger, sessionFinder SessionFinder) (*sessionMap, error) {
	logger = logger.Named("sessions")

	m := &sessionMap{
		deej:                   deej,
		logger:                 logger,
		m:                      make(map[string][]Session),
		lock:                   &sync.Mutex{},
		sessionFinder:          sessionFinder,
		sessionCountChangeChan: make(chan struct{}, 1),
	}

	logger.Debug("Created session map instance")

	return m, nil
}

func (m *sessionMap) SubscribeToSessionCountChange() <-chan struct{} {
	return m.sessionCountChangeChan
}

func (m *sessionMap) notifySessionCountChange() {
	select {
	case m.sessionCountChangeChan <- struct{}{}:
	default:
		// channel already has a pending notification
	}
}

func (m *sessionMap) initialize() error {
	m.setupOnSliderMove()

	// Check if the session finder supports event-driven tracking
	if eventDrivenFinder, ok := m.sessionFinder.(EventDrivenSessionFinder); ok {
		m.eventDriven = true
		// Setup event handler first - buffered events from init will be consumed
		m.setupOnSessionEvents(eventDrivenFinder)
		m.logger.Info("Using event-driven session tracking")
	} else {
		m.setupOnConfigReload()
		// Polling mode - get all sessions now
		if err := m.getAndAddSessions(); err != nil {
			m.logger.Warnw("Failed to get all sessions during session map initialization", "error", err)
			return fmt.Errorf("get all sessions during init: %w", err)
		}
		m.logger.Info("Using polling-based session tracking")
	}

	return nil
}

func (m *sessionMap) release() error {
	if err := m.sessionFinder.Release(); err != nil {
		m.logger.Warnw("Failed to release session finder during session map release", "error", err)
		return fmt.Errorf("release session finder during release: %w", err)
	}

	return nil
}

// assumes the session map is clean!
// only call on a new session map or as part of refreshSessions which calls reset
func (m *sessionMap) getAndAddSessions() error {

	// mark that we're refreshing before anything else
	m.lastSessionRefresh = time.Now()
	m.unmappedSessions = nil

	sessions, err := m.sessionFinder.GetAllSessions()
	if err != nil {
		m.logger.Warnw("Failed to get sessions from session finder", "error", err)
		return fmt.Errorf("get sessions from SessionFinder: %w", err)
	}

	for _, session := range sessions {
		m.add(session)

		if !m.sessionMapped(session) {
			m.logger.Debugw("Tracking unmapped session", "session", session)
			m.unmappedSessions = append(m.unmappedSessions, session)
		}
	}

	m.logger.Debugw("Got all audio sessions successfully", "sessionMap", m)

	m.notifySessionCountChange()

	return nil
}

func (m *sessionMap) setupOnConfigReload() {
	configReloadedChannel := m.deej.config.SubscribeToChanges()

	go func() {
		for {
			<-configReloadedChannel
			m.logger.Info("Detected config reload, attempting to re-acquire all audio sessions")
			m.refreshSessions(true) // force refresh on config reload
		}
	}()
}

func (m *sessionMap) setupOnSliderMove() {
	sliderEventsChannel := m.deej.serial.SubscribeToSliderMoveEvents()

	go func() {
		for {
			event := <-sliderEventsChannel
			m.handleSliderMoveEvent(event)
		}
	}()
}

func (m *sessionMap) setupOnSessionEvents(finder EventDrivenSessionFinder) {
	sessionEventsChan := finder.SubscribeToSessionEvents()

	go func() {
		for event := range sessionEventsChan {
			switch event.Type {
			case SessionEventAdded:
				m.handleSessionAdded(event)
			case SessionEventRemoved:
				m.handleSessionRemoved(event)
			}
		}
	}()
}

func (m *sessionMap) handleSessionAdded(event SessionEvent) {
	m.logger.Debugw("Session added event received", "session", event.Session)

	// Add to the main map
	m.add(event.Session)

	// Track as unmapped if applicable
	if !m.sessionMapped(event.Session) {
		m.logger.Debugw("Tracking unmapped session from event", "session", event.Session)
		m.lock.Lock()
		m.unmappedSessions = append(m.unmappedSessions, event.Session)
		m.lock.Unlock()
	}

	m.notifySessionCountChange()
}

func (m *sessionMap) handleSessionRemoved(event SessionEvent) {
	m.logger.Debugw("Session removed event received", "key", event.Session.Key())

	if event.Session == nil {
		return
	}

	// Remove from the main map
	m.removeSession(event.Session)

	// Remove from unmapped sessions if present
	m.lock.Lock()
	for i, unmapped := range m.unmappedSessions {
		if unmapped == event.Session {
			m.unmappedSessions = append(m.unmappedSessions[:i], m.unmappedSessions[i+1:]...)
			break
		}
	}
	m.lock.Unlock()

	m.notifySessionCountChange()
}

// removeSession removes a specific session from the map
func (m *sessionMap) removeSession(session Session) {
	m.lock.Lock()
	defer m.lock.Unlock()

	key := session.Key()
	sessions, ok := m.m[key]
	if !ok {
		return
	}

	// Find and remove the specific session
	for i, s := range sessions {
		if s == session {
			m.m[key] = append(sessions[:i], sessions[i+1:]...)
			break
		}
	}

	// Remove the key entirely if no sessions left
	if len(m.m[key]) == 0 {
		delete(m.m, key)
	}
}

// performance: explain why force == true at every such use to avoid unintended forced refresh spams
func (m *sessionMap) refreshSessions(force bool) {

	// make sure enough time passed since the last refresh, unless force is true in which case always clear
	if !force && m.lastSessionRefresh.Add(minTimeBetweenSessionRefreshes).After(time.Now()) {
		return
	}

	// clear and release sessions first
	m.clear()

	if err := m.getAndAddSessions(); err != nil {
		m.logger.Warnw("Failed to re-acquire all audio sessions", "error", err)
	} else {
		m.logger.Debug("Re-acquired sessions successfully")
	}
}

// returns true if a session is not currently mapped to any slider, false otherwise
// special sessions (master, system, mic) and device-specific sessions always count as mapped,
// even when absent from the config. this makes sense for every current feature that uses "unmapped sessions"
func (m *sessionMap) sessionMapped(session Session) bool {

	// count master/system/mic as mapped
	if funk.ContainsString([]string{masterSessionName, systemSessionName, inputSessionName}, session.Key()) {
		return true
	}

	// count device sessions as mapped
	if deviceSessionKeyPattern.MatchString(session.Key()) {
		return true
	}

	matchFound := false

	// look through the actual mappings
	m.deej.config.SliderMapping.iterate(func(_ int, targets []string) {
		for _, target := range targets {

			// ignore special transforms
			if m.targetHasSpecialTransform(target) {
				continue
			}

			// safe to assume this has a single element because we made sure there's no special transform
			target = m.resolveTarget(target)[0]

			if target == session.Key() {
				matchFound = true
				return
			}
		}
	})

	return matchFound
}

func (m *sessionMap) handleSliderMoveEvent(event SliderMoveEvent) {

	// get the targets mapped to this slider from the config
	targets, ok := m.deej.config.SliderMapping.get(event.SliderID)

	// if slider not found in config, silently ignore
	if !ok {
		return
	}

	targetFound := false
	adjustmentFailed := false

	// for each possible target for this slider...
	for _, target := range targets {

		// resolve the target name by cleaning it up and applying any special transformations.
		// depending on the transformation applied, this can result in more than one target name
		resolvedTargets := m.resolveTarget(target)

		// for each resolved target...
		for _, resolvedTarget := range resolvedTargets {

			// check the map for matching sessions
			sessions, ok := m.get(resolvedTarget)

			// no sessions matching this target - move on
			if !ok {
				continue
			}

			targetFound = true

			// iterate all matching sessions and adjust the volume of each one
			for _, session := range sessions {
				if session.GetVolume() != event.PercentValue {
					if err := session.SetVolume(event.PercentValue); err != nil {
						m.logger.Warnw("Failed to set target session volume", "error", err)
						adjustmentFailed = true
					}
				}
			}
		}
	}

	// if we still haven't found a target or the volume adjustment failed, maybe look for the target again.
	// processes could've opened since the last time this slider moved.
	// if they haven't, the cooldown will take care to not spam it up
	// Note: In event-driven mode, we don't refresh on target not found since events handle new sessions
	if !targetFound && !m.eventDriven {
		m.refreshSessions(false)
	} else if adjustmentFailed {

		// performance: the reason that forcing a refresh here is okay is that we'll only get here
		// when a session's SetVolume call errored, such as in the case of a stale master session
		// (or another, more catastrophic failure happens)
		m.refreshSessions(true)
	}
}

func (m *sessionMap) targetHasSpecialTransform(target string) bool {
	return strings.HasPrefix(target, specialTargetTransformPrefix)
}

func (m *sessionMap) resolveTarget(target string) []string {

	// start by ignoring the case
	target = strings.ToLower(target)

	// look for any special targets first, by examining the prefix
	if m.targetHasSpecialTransform(target) {
		return m.applyTargetTransform(strings.TrimPrefix(target, specialTargetTransformPrefix))
	}

	return []string{target}
}

func (m *sessionMap) applyTargetTransform(specialTargetName string) []string {
	checkFullscreen := false

	// select the transformation based on its name
	switch specialTargetName {

	// get current active fullscreen window
	case specialTargetCurrentFullscreenWindow:
		checkFullscreen = true
		fallthrough

	// get current active window
	case specialTargetCurrentWindow:
		currentWindowProcessNames, err := util.GetCurrentWindowProcessNames(checkFullscreen)

		// silently ignore errors here, as this is on deej's "hot path" (and it could just mean the user's running linux)
		if err != nil {
			return nil
		}

		// we could have gotten a non-lowercase names from that, so let's ensure we return ones that are lowercase
		for targetIdx, target := range currentWindowProcessNames {
			currentWindowProcessNames[targetIdx] = strings.ToLower(target)
		}

		// remove dupes
		return funk.UniqString(currentWindowProcessNames)

	// get currently unmapped sessions
	case specialTargetAllUnmapped:
		targetKeys := make([]string, len(m.unmappedSessions))
		for sessionIdx, session := range m.unmappedSessions {
			targetKeys[sessionIdx] = session.Key()
		}

		return targetKeys
	}

	return nil
}

func (m *sessionMap) add(value Session) {
	m.lock.Lock()
	defer m.lock.Unlock()

	key := value.Key()

	existing, ok := m.m[key]
	if !ok {
		m.m[key] = []Session{value}
	} else {
		m.m[key] = append(existing, value)
	}
}

func (m *sessionMap) get(key string) ([]Session, bool) {
	m.lock.Lock()
	defer m.lock.Unlock()

	value, ok := m.m[key]
	return value, ok
}

func (m *sessionMap) clear() {
	m.lock.Lock()
	defer m.lock.Unlock()

	m.logger.Debug("Releasing and clearing all audio sessions")

	// Don't release sessions in event-driven mode - the session finder manages their lifecycle
	if !m.eventDriven {
		for _, sessions := range m.m {
			for _, session := range sessions {
				session.Release()
			}
		}
	}

	// Clear the map
	m.m = make(map[string][]Session)

	m.logger.Debug("Session map cleared")
}

func (m *sessionMap) String() string {
	return fmt.Sprintf("<%d audio sessions>", m.getSessionCount())
}

func (m *sessionMap) getSessionCount() int {
	m.lock.Lock()
	defer m.lock.Unlock()

	sessionCount := 0

	for _, value := range m.m {
		sessionCount += len(value)
	}

	return sessionCount
}
