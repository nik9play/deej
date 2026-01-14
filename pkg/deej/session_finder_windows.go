package deej

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	ole "github.com/go-ole/go-ole"
	wca "github.com/moutend/go-wca/pkg/wca"
	"go.uber.org/zap"
)

type wcaSessionFinder struct {
	logger        *zap.SugaredLogger
	sessionLogger *zap.SugaredLogger

	eventCtx *ole.GUID // needed for some session actions to successfully notify other audio consumers

	// needed for device change notifications
	mmDeviceEnumerator      *wca.IMMDeviceEnumerator
	mmNotificationClient    *wca.IMMNotificationClient
	lastDefaultDeviceChange time.Time

	// our master input and output sessions
	masterOut   *masterSession
	masterIn    *masterSession
	masterOutID string // session ID for event tracking
	masterInID  string // session ID for event tracking

	// per-device session managers (persistent)
	deviceManagers     map[string]*deviceSessionManager
	deviceManagersLock sync.RWMutex

	// tracked sessions with their event callbacks
	trackedSessions     map[string]*trackedSession
	trackedSessionsLock sync.RWMutex

	// event channels
	sessionEventChan chan SessionEvent

	// ready channel - closed when initialization is complete
	ready    chan struct{}
	initErr  error
	initOnce sync.Once

	lock sync.Mutex

	workerCtx    context.Context
	workerCancel context.CancelFunc
}

type getSessionsResult struct {
	sessions []Session
	err      error
}

// deviceSessionManager holds persistent references for a single audio device
type deviceSessionManager struct {
	deviceID            string
	device              *wca.IMMDevice
	sessionManager      *wca.IAudioSessionManager2
	sessionNotification *IAudioSessionNotification
	masterSession       *masterSession // device-specific master volume session
	isOutput            bool           // only output devices have process sessions
}

// trackedSession holds a session with its registered event callback
type trackedSession struct {
	session       Session
	eventCallback *IAudioSessionEvents
	control       *wca.IAudioSessionControl
}

const (
	// there's no real mystery here, it's just a random GUID
	myteriousGUID = "{1ec920a1-7db8-44ba-9779-e5d28ed9f330}"

	// the notification client will call this multiple times in quick succession based on the
	// default device's assigned media roles, so we need to filter out the extraneous calls
	minDefaultDeviceChangeThreshold = 100 * time.Millisecond

	// prefix for device sessions in logger
	deviceSessionFormat = "device.%s"

	// buffer size for session event channel
	sessionEventChanSize = 100
)

func newSessionFinder(logger *zap.SugaredLogger) (SessionFinder, error) {
	ctx, cancel := context.WithCancel(context.Background())

	sf := &wcaSessionFinder{
		logger:           logger.Named("session_finder"),
		sessionLogger:    logger.Named("sessions"),
		eventCtx:         ole.NewGUID(myteriousGUID),
		deviceManagers:   make(map[string]*deviceSessionManager),
		trackedSessions:  make(map[string]*trackedSession),
		sessionEventChan: make(chan SessionEvent, sessionEventChanSize),
		ready:            make(chan struct{}),
		workerCtx:        ctx,
		workerCancel:     cancel,
	}

	sf.logger.Debug("Created WCA session finder instance")

	go sf.sessionFinderWorker(ctx)

	return sf, nil
}

// I noticed that deej crashes with E_INVALIDARG when trying to run it in VM over RDP
// It turned out that the problem was an incorrectly passed ctx argument in the go-wca library
func mmdActivateWorkaround(mmd *wca.IMMDevice, refIID *ole.GUID, ctx uint32, _, obj interface{}) (err error) {
	objValue := reflect.ValueOf(obj).Elem()
	hr, _, _ := syscall.SyscallN(
		mmd.VTable().Activate,
		uintptr(unsafe.Pointer(mmd)),
		uintptr(unsafe.Pointer(refIID)),
		uintptr(ctx),
		0,
		objValue.Addr().Pointer())
	if hr != 0 {
		err = ole.NewError(hr)
	}
	return
}

func (sf *wcaSessionFinder) initializeCOMLoop(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			sf.logger.Info("COM initializing stopping")
			return errors.New("com initializing stopped")
		default:
			err := ole.CoInitializeEx(0, ole.COINIT_MULTITHREADED)

			if err == nil {
				return nil
			}

			const eFalse = 1
			oleError := &ole.OleError{}

			if errors.As(err, &oleError) {
				if oleError.Code() == eFalse {
					return nil
				}
				sf.logger.Warnw("Failed to call CoInitializeEx. Retrying...",
					"isOleError", true,
					"error", err,
					"oleError", oleError)

				time.Sleep(2 * time.Second)
				continue
			}
			sf.logger.Warnw("Failed to call CoInitializeEx. Retrying...",
				"isOleError", false,
				"error", err,
				"oleError", nil)

			time.Sleep(2 * time.Second)
		}
	}
}

func (sf *wcaSessionFinder) sessionFinderWorker(ctx context.Context) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	// Helper to signal initialization complete (success or failure)
	signalReady := func(err error) {
		sf.initOnce.Do(func() {
			sf.initErr = err
			close(sf.ready)
		})
	}

	if err := sf.initializeCOMLoop(ctx); err != nil {
		signalReady(fmt.Errorf("COM initialization failed: %w", err))
		return
	}
	sf.logger.Info("COM initialized for session finder")
	defer ole.CoUninitialize()

	// Initialize device enumerator and register for device notifications
	if err := sf.initializeDeviceEnumerator(); err != nil {
		sf.logger.Errorw("Failed to initialize device enumerator", "error", err)
		signalReady(fmt.Errorf("device enumerator initialization failed: %w", err))
		return
	}
	// Initialize all device managers and register for session notifications
	if err := sf.initializeAllDeviceManagers(); err != nil {
		sf.logger.Warnw("Failed to initialize device managers", "error", err)
	}

	// Initialize master sessions
	sf.initializeMasterSessions()

	// Signal that initialization is complete
	signalReady(nil)

	sf.logger.Debug("Event-driven session finder initialized")

	<-ctx.Done()
	sf.logger.Info("Session finder worker stopping")
	sf.cleanup()
}

func (sf *wcaSessionFinder) initializeDeviceEnumerator() error {
	if err := wca.CoCreateInstance(
		wca.CLSID_MMDeviceEnumerator,
		0,
		wca.CLSCTX_ALL,
		wca.IID_IMMDeviceEnumerator,
		&sf.mmDeviceEnumerator,
	); err != nil {
		return fmt.Errorf("create device enumerator: %w", err)
	}

	// Register for device change notifications
	callback := wca.IMMNotificationClientCallback{
		OnDefaultDeviceChanged: sf.defaultDeviceChangedCallback,
		OnDeviceAdded:          sf.deviceAddedCallback,
		OnDeviceRemoved:        sf.deviceRemovedCallback,
		OnDeviceStateChanged:   sf.deviceStateChangedCallback,
	}

	sf.mmNotificationClient = wca.NewIMMNotificationClient(callback)

	if err := sf.mmDeviceEnumerator.RegisterEndpointNotificationCallback(sf.mmNotificationClient); err != nil {
		return fmt.Errorf("register endpoint notification callback: %w", err)
	}

	return nil
}

func (sf *wcaSessionFinder) initializeAllDeviceManagers() error {
	var deviceCollection *wca.IMMDeviceCollection

	if err := sf.mmDeviceEnumerator.EnumAudioEndpoints(wca.EAll, wca.DEVICE_STATE_ACTIVE, &deviceCollection); err != nil {
		return fmt.Errorf("enumerate audio endpoints: %w", err)
	}
	defer deviceCollection.Release()

	var deviceCount uint32
	if err := deviceCollection.GetCount(&deviceCount); err != nil {
		return fmt.Errorf("get device count: %w", err)
	}

	for i := uint32(0); i < deviceCount; i++ {
		var device *wca.IMMDevice
		if err := deviceCollection.Item(i, &device); err != nil {
			sf.logger.Warnw("Failed to get device from collection", "index", i, "error", err)
			continue
		}

		if err := sf.createDeviceManager(device); err != nil {
			sf.logger.Warnw("Failed to create device manager", "index", i, "error", err)
			device.Release()
		}
	}

	return nil
}

func (sf *wcaSessionFinder) initializeMasterSessions() {
	sf.refreshMasterOutput()
	sf.refreshMasterInput()
}

func (sf *wcaSessionFinder) emitSessionEvent(event SessionEvent) {
	select {
	case sf.sessionEventChan <- event:
	default:
		sf.logger.Warnw("Session event channel full, dropping event", "type", event.Type, "sessionID", event.SessionID)
	}
}

func (sf *wcaSessionFinder) createDeviceManager(device *wca.IMMDevice) error {
	// Get device ID
	var deviceIDStr string
	if err := device.GetId(&deviceIDStr); err != nil {
		return fmt.Errorf("get device ID: %w", err)
	}

	sf.deviceManagersLock.Lock()
	defer sf.deviceManagersLock.Unlock()

	// Check if we already have a manager for this device
	if _, exists := sf.deviceManagers[deviceIDStr]; exists {
		device.Release()
		return nil
	}

	// Get endpoint type to determine if it's an output device
	dispatch, err := device.QueryInterface(wca.IID_IMMEndpoint)
	if err != nil {
		return fmt.Errorf("query IMMEndpoint: %w", err)
	}
	endpoint := (*wca.IMMEndpoint)(unsafe.Pointer(dispatch))
	defer endpoint.Release()

	var dataFlow uint32
	if err := endpoint.GetDataFlow(&dataFlow); err != nil {
		return fmt.Errorf("get data flow: %w", err)
	}

	isOutput := dataFlow == wca.ERender

	// Activate IAudioSessionManager2
	var sessionManager *wca.IAudioSessionManager2
	if err := mmdActivateWorkaround(device, wca.IID_IAudioSessionManager2, wca.CLSCTX_ALL, nil, &sessionManager); err != nil {
		return fmt.Errorf("activate session manager: %w", err)
	}

	dm := &deviceSessionManager{
		deviceID:       deviceIDStr,
		device:         device,
		sessionManager: sessionManager,
		isOutput:       isOutput,
	}

	// Create device master session
	deviceMasterSession, err := sf.createDeviceMasterSession(device)
	if err != nil {
		sf.logger.Warnw("Failed to create device master session", "deviceID", deviceIDStr, "error", err)
	} else {
		dm.masterSession = deviceMasterSession
		// Emit event for device master session
		sf.emitSessionEvent(SessionEvent{Type: SessionEventAdded, Session: deviceMasterSession, SessionID: "device_" + deviceIDStr})
	}

	// Only register for session notifications on output devices (they have process sessions)
	if isOutput {
		notificationCallback := IAudioSessionNotificationCallback{
			OnSessionCreated: func(newSession *wca.IAudioSessionControl) error {
				return sf.onSessionCreated(deviceIDStr, newSession)
			},
		}

		dm.sessionNotification = NewIAudioSessionNotification(notificationCallback)

		if err := sessionManager.RegisterSessionNotification(dm.sessionNotification.ToWCA()); err != nil {
			sf.logger.Warnw("Failed to register session notification", "deviceID", deviceIDStr, "error", err)
		} else {
			sf.logger.Debugw("Registered session notification for device", "deviceID", deviceIDStr)
		}

		// Enumerate existing sessions on this device
		sf.enumerateDeviceSessions(dm)
	}

	sf.deviceManagers[deviceIDStr] = dm
	sf.logger.Debugw("Created device manager", "deviceID", deviceIDStr, "isOutput", isOutput)

	return nil
}

func (sf *wcaSessionFinder) enumerateDeviceSessions(dm *deviceSessionManager) {
	var sessionEnumerator *wca.IAudioSessionEnumerator
	if err := dm.sessionManager.GetSessionEnumerator(&sessionEnumerator); err != nil {
		sf.logger.Warnw("Failed to get session enumerator", "deviceID", dm.deviceID, "error", err)
		return
	}
	defer sessionEnumerator.Release()

	var sessionCount int
	if err := sessionEnumerator.GetCount(&sessionCount); err != nil {
		sf.logger.Warnw("Failed to get session count", "deviceID", dm.deviceID, "error", err)
		return
	}

	for i := 0; i < sessionCount; i++ {
		var audioSessionControl *wca.IAudioSessionControl
		if err := sessionEnumerator.GetSession(i, &audioSessionControl); err != nil {
			sf.logger.Warnw("Failed to get session", "deviceID", dm.deviceID, "index", i, "error", err)
			continue
		}

		if err := sf.addSessionFromControl(dm.deviceID, audioSessionControl); err != nil {
			sf.logger.Debugw("Failed to add session from control", "deviceID", dm.deviceID, "index", i, "error", err)
		}
	}
}

func (sf *wcaSessionFinder) onSessionCreated(deviceID string, newSession *wca.IAudioSessionControl) error {
	sf.logger.Debugw("New session created callback", "deviceID", deviceID)

	// AddRef because Windows will release the passed reference after callback returns
	newSession.AddRef()

	if err := sf.addSessionFromControl(deviceID, newSession); err != nil {
		sf.logger.Debugw("Failed to add new session", "deviceID", deviceID, "error", err)
		newSession.Release()
	}

	return nil
}

func (sf *wcaSessionFinder) addSessionFromControl(deviceID string, audioSessionControl *wca.IAudioSessionControl) error {
	// Query IAudioSessionControl2
	dispatch, err := audioSessionControl.QueryInterface(wca.IID_IAudioSessionControl2)
	if err != nil {
		audioSessionControl.Release()
		return fmt.Errorf("query IAudioSessionControl2: %w", err)
	}
	audioSessionControl2 := (*wca.IAudioSessionControl2)(unsafe.Pointer(dispatch))

	// Get PID
	var pid uint32
	if err := audioSessionControl2.GetProcessId(&pid); err != nil {
		isSystemSoundsErr := audioSessionControl2.IsSystemSoundsSession()
		if isSystemSoundsErr != nil && !strings.Contains(err.Error(), "143196173") {
			audioSessionControl2.Release()
			audioSessionControl.Release()
			return fmt.Errorf("get process ID: %w", err)
		}
	}

	// Query ISimpleAudioVolume
	dispatch, err = audioSessionControl2.QueryInterface(wca.IID_ISimpleAudioVolume)
	if err != nil {
		audioSessionControl2.Release()
		audioSessionControl.Release()
		return fmt.Errorf("query ISimpleAudioVolume: %w", err)
	}
	simpleAudioVolume := (*wca.ISimpleAudioVolume)(unsafe.Pointer(dispatch))

	// Create session
	session, err := newWCASession(sf.sessionLogger, audioSessionControl2, simpleAudioVolume, pid, sf.eventCtx)
	if err != nil {
		audioSessionControl2.Release()
		simpleAudioVolume.Release()
		audioSessionControl.Release()

		if !errors.Is(err, errNoSuchProcess) {
			return fmt.Errorf("create session: %w", err)
		}
		return errNoSuchProcess
	}

	sessionID := fmt.Sprintf("%s_%s_%d", deviceID, session.Key(), pid)

	// Register session events callback
	eventsCallback := IAudioSessionEventsCallback{
		OnStateChanged: func(newState uint32) error {
			if newState == wca.AudioSessionStateExpired {
				sf.logger.Debugw("Session state changed to expired", "sessionID", sessionID)
				sf.removeSession(sessionID)
			}
			return nil
		},
		OnSessionDisconnected: func(disconnectReason uint32) error {
			sf.logger.Debugw("Session disconnected", "sessionID", sessionID, "reason", disconnectReason)
			sf.removeSession(sessionID)
			return nil
		},
	}

	sessionEvents := NewIAudioSessionEvents(eventsCallback)

	if err := audioSessionControl.RegisterAudioSessionNotification(sessionEvents.ToWCA()); err != nil {
		sf.logger.Warnw("Failed to register audio session notification", "sessionID", sessionID, "error", err)
	}

	sf.trackedSessionsLock.Lock()
	// Check if session already exists
	if _, exists := sf.trackedSessions[sessionID]; exists {
		sf.trackedSessionsLock.Unlock()
		session.Release()
		audioSessionControl.Release()
		return nil
	}

	sf.trackedSessions[sessionID] = &trackedSession{
		session:       session,
		eventCallback: sessionEvents,
		control:       audioSessionControl,
	}
	sf.trackedSessionsLock.Unlock()

	sf.emitSessionEvent(SessionEvent{Type: SessionEventAdded, Session: session, SessionID: sessionID})

	sf.logger.Debugw("Added tracked session", "sessionID", sessionID, "key", session.Key())
	return nil
}

func (sf *wcaSessionFinder) removeSession(sessionID string) {
	sf.trackedSessionsLock.Lock()
	tracked, exists := sf.trackedSessions[sessionID]
	if !exists {
		sf.trackedSessionsLock.Unlock()
		return
	}
	delete(sf.trackedSessions, sessionID)
	sf.trackedSessionsLock.Unlock()

	// Unregister event callback
	if tracked.control != nil && tracked.eventCallback != nil {
		if err := tracked.control.UnregisterAudioSessionNotification(tracked.eventCallback.ToWCA()); err != nil {
			sf.logger.Debugw("Failed to unregister audio session notification", "sessionID", sessionID, "error", err)
		}
		tracked.control.Release()
	}

	sf.emitSessionEvent(SessionEvent{Type: SessionEventRemoved, SessionID: sessionID, Session: tracked.session})

	tracked.session.Release()
	sf.logger.Debugw("Removed tracked session", "sessionID", sessionID)
}

func (sf *wcaSessionFinder) removeDeviceManager(deviceID string) {
	sf.deviceManagersLock.Lock()
	dm, exists := sf.deviceManagers[deviceID]
	if !exists {
		sf.deviceManagersLock.Unlock()
		return
	}
	delete(sf.deviceManagers, deviceID)
	sf.deviceManagersLock.Unlock()

	// Remove all sessions associated with this device
	sf.trackedSessionsLock.RLock()
	sessionsToRemove := make([]string, 0)
	for sessionID := range sf.trackedSessions {
		if strings.HasPrefix(sessionID, deviceID+"_") {
			sessionsToRemove = append(sessionsToRemove, sessionID)
		}
	}
	sf.trackedSessionsLock.RUnlock()

	for _, sessionID := range sessionsToRemove {
		sf.removeSession(sessionID)
	}

	// Cleanup device manager
	if dm.sessionNotification != nil && dm.sessionManager != nil {
		if err := dm.sessionManager.UnregisterSessionNotification(dm.sessionNotification.ToWCA()); err != nil {
			sf.logger.Debugw("Failed to unregister session notification", "deviceID", deviceID, "error", err)
		}
	}

	sf.emitSessionEvent(SessionEvent{Type: SessionEventRemoved, Session: dm.masterSession, SessionID: "device_" + deviceID})

	if dm.masterSession != nil {
		dm.masterSession.Release()
	}
	if dm.sessionManager != nil {
		dm.sessionManager.Release()
	}
	if dm.device != nil {
		dm.device.Release()
	}

	sf.logger.Debugw("Removed device manager", "deviceID", deviceID)
}

func (sf *wcaSessionFinder) cleanup() {
	sf.deviceManagersLock.Lock()
	deviceIDs := make([]string, 0, len(sf.deviceManagers))
	for id := range sf.deviceManagers {
		deviceIDs = append(deviceIDs, id)
	}
	sf.deviceManagersLock.Unlock()

	for _, id := range deviceIDs {
		sf.removeDeviceManager(id)
	}

	if sf.mmDeviceEnumerator != nil {
		sf.mmDeviceEnumerator.Release()
	}
}

// GetAllSessions returns all currently tracked sessions
// This is called once at startup to get the initial session list
func (sf *wcaSessionFinder) GetAllSessions() ([]Session, error) {
	// Wait for initialization to complete
	<-sf.ready
	if sf.initErr != nil {
		return nil, sf.initErr
	}

	return sf.getAllSessionsInternal()
}

// getAllSessionsInternal returns all currently tracked sessions
func (sf *wcaSessionFinder) getAllSessionsInternal() ([]Session, error) {
	sessions := []Session{}

	// Add master sessions
	sf.lock.Lock()
	if sf.masterOut != nil {
		sessions = append(sessions, sf.masterOut)
	}
	if sf.masterIn != nil {
		sessions = append(sessions, sf.masterIn)
	}
	sf.lock.Unlock()

	// Add device master sessions
	sf.deviceManagersLock.RLock()
	for _, dm := range sf.deviceManagers {
		if dm.masterSession != nil {
			sessions = append(sessions, dm.masterSession)
		}
	}
	sf.deviceManagersLock.RUnlock()

	// Add all tracked process sessions
	sf.trackedSessionsLock.RLock()
	for _, tracked := range sf.trackedSessions {
		sessions = append(sessions, tracked.session)
	}
	sf.trackedSessionsLock.RUnlock()

	return sessions, nil
}

func (sf *wcaSessionFinder) createDeviceMasterSession(device *wca.IMMDevice) (*masterSession, error) {
	// Get device properties for friendly name
	var propertyStore *wca.IPropertyStore
	if err := device.OpenPropertyStore(wca.STGM_READ, &propertyStore); err != nil {
		return nil, fmt.Errorf("open property store: %w", err)
	}
	defer propertyStore.Release()

	value := &wca.PROPVARIANT{}
	if err := propertyStore.GetValue(&wca.PKEY_Device_DeviceDesc, value); err != nil {
		return nil, fmt.Errorf("get device description: %w", err)
	}
	endpointDescription := strings.ToLower(value.String())

	if err := propertyStore.GetValue(&wca.PKEY_Device_FriendlyName, value); err != nil {
		return nil, fmt.Errorf("get friendly name: %w", err)
	}
	endpointFriendlyName := value.String()

	return sf.getMasterSession(device, endpointFriendlyName, fmt.Sprintf(deviceSessionFormat, endpointDescription))
}

func (sf *wcaSessionFinder) getMasterSession(mmDevice *wca.IMMDevice, key string, loggerKey string) (*masterSession, error) {
	var audioEndpointVolume *wca.IAudioEndpointVolume

	if err := mmdActivateWorkaround(mmDevice, wca.IID_IAudioEndpointVolume, wca.CLSCTX_ALL, nil, &audioEndpointVolume); err != nil {
		return nil, fmt.Errorf("activate AudioEndpointVolume: %w", err)
	}

	master, err := newMasterSession(sf.sessionLogger, audioEndpointVolume, sf.eventCtx, key, loggerKey)
	if err != nil {
		audioEndpointVolume.Release()
		return nil, fmt.Errorf("create master session: %w", err)
	}

	return master, nil
}

// SubscribeToSessionEvents returns a channel for session add/remove events
func (sf *wcaSessionFinder) SubscribeToSessionEvents() <-chan SessionEvent {
	return sf.sessionEventChan
}

func (sf *wcaSessionFinder) Release() error {
	sf.workerCancel()

	if sf.mmDeviceEnumerator != nil {
		sf.mmDeviceEnumerator.Release()
	}

	sf.logger.Debug("Released WCA session finder instance")
	return nil
}

func (sf *wcaSessionFinder) defaultDeviceChangedCallback(
	flow wca.EDataFlow, role wca.ERole, pwstrDeviceID string,
) error {
	now := time.Now()

	if sf.lastDefaultDeviceChange.Add(minDefaultDeviceChangeThreshold).After(now) {
		return nil
	}

	sf.lastDefaultDeviceChange = now

	sf.logger.Debugw("Default audio device changed", "flow", flow, "role", role, "deviceID", pwstrDeviceID)

	// Handle output device change
	if flow == wca.ERender || flow == wca.EAll {
		sf.refreshMasterOutput()
	}

	// Handle input device change
	if flow == wca.ECapture || flow == wca.EAll {
		sf.refreshMasterInput()
	}

	return nil
}

func (sf *wcaSessionFinder) refreshMasterOutput() {
	sf.lock.Lock()
	defer sf.lock.Unlock()

	// Remove old master output session
	if sf.masterOut != nil {
		sf.logger.Debug("Removing old master output session")

		sf.emitSessionEvent(SessionEvent{Type: SessionEventRemoved, SessionID: sf.masterOutID, Session: sf.masterOut})

		sf.masterOut.Release()
		sf.masterOut = nil
		sf.masterOutID = ""
	}

	// Get new default output device
	var mmOutDevice *wca.IMMDevice
	if err := sf.mmDeviceEnumerator.GetDefaultAudioEndpoint(wca.ERender, wca.EConsole, &mmOutDevice); err != nil {
		sf.logger.Warnw("Failed to get new default output endpoint", "error", err)
		return
	}
	defer mmOutDevice.Release()

	// Create new master output session
	masterOut, err := sf.getMasterSession(mmOutDevice, masterSessionName, masterSessionName)
	if err != nil {
		sf.logger.Warnw("Failed to create new master output session", "error", err)
		return
	}

	sf.masterOut = masterOut
	sf.masterOutID = "master_output"

	sf.emitSessionEvent(SessionEvent{Type: SessionEventAdded, Session: masterOut, SessionID: sf.masterOutID})

	sf.logger.Debug("Refreshed master output session for new default device")
}

func (sf *wcaSessionFinder) refreshMasterInput() {
	sf.lock.Lock()
	defer sf.lock.Unlock()

	// Remove old master input session
	if sf.masterIn != nil {
		sf.logger.Debug("Removing old master input session")

		sf.emitSessionEvent(SessionEvent{Type: SessionEventRemoved, SessionID: sf.masterInID, Session: sf.masterIn})

		sf.masterIn.Release()
		sf.masterIn = nil
		sf.masterInID = ""
	}

	// Get new default input device
	var mmInDevice *wca.IMMDevice
	if err := sf.mmDeviceEnumerator.GetDefaultAudioEndpoint(wca.ECapture, wca.EConsole, &mmInDevice); err != nil {
		sf.logger.Debug("No default input device available after change")
		return
	}
	defer mmInDevice.Release()

	// Create new master input session
	masterIn, err := sf.getMasterSession(mmInDevice, inputSessionName, inputSessionName)
	if err != nil {
		sf.logger.Warnw("Failed to create new master input session", "error", err)
		return
	}

	sf.masterIn = masterIn
	sf.masterInID = "master_input"

	sf.emitSessionEvent(SessionEvent{Type: SessionEventAdded, Session: masterIn, SessionID: sf.masterInID})

	sf.logger.Debug("Refreshed master input session for new default device")
}

func (sf *wcaSessionFinder) deviceAddedCallback(pwstrDeviceID string) error {
	sf.logger.Debugw("Device added", "deviceID", pwstrDeviceID)
	sf.handleDeviceAdded(pwstrDeviceID)

	return nil
}

func (sf *wcaSessionFinder) handleDeviceAdded(pwstrDeviceID string) {
	// Find the device by enumerating all devices and matching the ID
	// go-wca's GetDevice isn't properly implemented, so we use this workaround
	device, err := sf.findDeviceByID(pwstrDeviceID)
	if err != nil {
		sf.logger.Warnw("Failed to find added device", "deviceID", pwstrDeviceID, "error", err)
		return
	}

	if device == nil {
		sf.logger.Debugw("Device not found in enumeration", "deviceID", pwstrDeviceID)
		return
	}

	if err := sf.createDeviceManager(device); err != nil {
		sf.logger.Warnw("Failed to create device manager for added device", "deviceID", pwstrDeviceID, "error", err)
		device.Release()
	}
}

func (sf *wcaSessionFinder) findDeviceByID(targetDeviceID string) (*wca.IMMDevice, error) {
	if sf.mmDeviceEnumerator == nil {
		return nil, errors.New("device enumerator not initialized")
	}

	var deviceCollection *wca.IMMDeviceCollection
	if err := sf.mmDeviceEnumerator.EnumAudioEndpoints(wca.EAll, wca.DEVICE_STATE_ACTIVE, &deviceCollection); err != nil {
		return nil, fmt.Errorf("enumerate audio endpoints: %w", err)
	}
	defer deviceCollection.Release()

	var deviceCount uint32
	if err := deviceCollection.GetCount(&deviceCount); err != nil {
		return nil, fmt.Errorf("get device count: %w", err)
	}

	for i := uint32(0); i < deviceCount; i++ {
		var device *wca.IMMDevice
		if err := deviceCollection.Item(i, &device); err != nil {
			continue
		}

		var deviceID string
		if err := device.GetId(&deviceID); err != nil {
			device.Release()
			continue
		}

		if deviceID == targetDeviceID {
			return device, nil
		}

		device.Release()
	}

	return nil, nil
}

func (sf *wcaSessionFinder) deviceRemovedCallback(pwstrDeviceID string) error {
	sf.logger.Debugw("Device removed", "deviceID", pwstrDeviceID)
	sf.removeDeviceManager(pwstrDeviceID)

	return nil
}

func (sf *wcaSessionFinder) deviceStateChangedCallback(pwstrDeviceID string, dwNewState uint64) error {
	sf.logger.Debugw("Device state changed", "deviceID", pwstrDeviceID, "newState", dwNewState)

	dm, exists := sf.deviceManagers[pwstrDeviceID]

	if exists {
		// dwNewState is always 0 because of bug in go-wca IMMNotificationClient implementation
		// So we need to query the device for its actual state
		// TODO: fork go-wca and fix this and other issues
		var state uint32
		dm.device.GetState(&state)

		if dwNewState != wca.DEVICE_STATE_ACTIVE {
			// Device became active, try to create a manager
			sf.removeDeviceManager(pwstrDeviceID)
		}
	} else {
		sf.handleDeviceAdded(pwstrDeviceID)
	}

	return nil
}
