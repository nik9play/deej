package deej

import (
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/jfreymuth/pulse/proto"
	"go.uber.org/zap"
)

const (
	sessionEventChanSize = 100
	reconnectDelay       = 2 * time.Second
)

type paSessionFinder struct {
	logger        *zap.SugaredLogger
	sessionLogger *zap.SugaredLogger

	mu           sync.RWMutex
	client       *proto.Client
	conn         net.Conn
	masterSink   *masterSession
	masterSource *masterSession
	sinkInputs   map[uint32]*paSession
	closed       bool

	sessionEvents chan SessionEvent
}

func newSessionFinder(logger *zap.SugaredLogger) (SessionFinder, error) {
	sf := &paSessionFinder{
		logger:        logger.Named("session_finder"),
		sessionLogger: logger.Named("sessions"),
		sinkInputs:    make(map[uint32]*paSession),
		sessionEvents: make(chan SessionEvent, sessionEventChanSize),
	}

	if err := sf.connect(); err != nil {
		return nil, err
	}

	sf.logger.Debug("Created event-driven PA session finder")
	return sf, nil
}

func (sf *paSessionFinder) connect() error {
	client, conn, err := proto.Connect("")
	if err != nil {
		return fmt.Errorf("connect to PulseAudio: %w", err)
	}

	if err := client.Request(&proto.SetClientName{
		Props: proto.PropList{
			"application.name": proto.PropListString("deej"),
		},
	}, &proto.SetClientNameReply{}); err != nil {
		conn.Close()
		return fmt.Errorf("set client name: %w", err)
	}

	sf.mu.Lock()
	sf.client = client
	sf.conn = conn
	sf.mu.Unlock()

	sf.refreshMaster()
	sf.enumerateExistingSessions()

	client.Callback = sf.onPulseEvent
	if err := client.Request(&proto.Subscribe{
		Mask: proto.SubscriptionMaskSinkInput | proto.SubscriptionMaskServer,
	}, nil); err != nil {
		conn.Close()
		return fmt.Errorf("subscribe to events: %w", err)
	}

	return nil
}

func (sf *paSessionFinder) reconnect() {
	sf.mu.Lock()
	if sf.closed {
		sf.mu.Unlock()
		return
	}

	// Clear old sessions
	for _, s := range sf.sinkInputs {
		sf.emitEvent(SessionEvent{Type: SessionEventRemoved, Session: s})
		s.Release()
	}
	sf.sinkInputs = make(map[uint32]*paSession)

	if sf.masterSink != nil {
		sf.emitEvent(SessionEvent{Type: SessionEventRemoved, Session: sf.masterSink})
		sf.masterSink.Release()
		sf.masterSink = nil
	}
	if sf.masterSource != nil {
		sf.emitEvent(SessionEvent{Type: SessionEventRemoved, Session: sf.masterSource})
		sf.masterSource.Release()
		sf.masterSource = nil
	}

	if sf.conn != nil {
		sf.conn.Close()
	}
	sf.client = nil
	sf.conn = nil
	sf.mu.Unlock()

	// Retry until successful
	for {
		sf.logger.Info("Attempting to reconnect to PulseAudio...")
		if err := sf.connect(); err != nil {
			sf.logger.Warnw("Reconnect failed, retrying", "error", err)
			time.Sleep(reconnectDelay)

			sf.mu.RLock()
			closed := sf.closed
			sf.mu.RUnlock()
			if closed {
				return
			}
			continue
		}
		sf.logger.Info("Reconnected to PulseAudio")
		return
	}
}

func (sf *paSessionFinder) onPulseEvent(msg interface{}) {
	switch v := msg.(type) {
	case *proto.SubscribeEvent:
		facility := v.Event.GetFacility()
		eventType := v.Event.GetType()

		switch facility {
		case proto.EventSinkSinkInput:
			go sf.handleSinkInputEvent(eventType, v.Index)
		case proto.EventServer:
			go sf.refreshMaster()
		}

	case *proto.ConnectionClosed:
		sf.logger.Warn("PulseAudio connection closed")
		go sf.reconnect()
	}
}

func (sf *paSessionFinder) handleSinkInputEvent(eventType proto.SubscriptionEventType, index uint32) {
	switch eventType {
	case proto.EventNew:
		sf.addSinkInput(index)
	case proto.EventRemove:
		sf.removeSinkInput(index)
	}
}

func (sf *paSessionFinder) refreshMaster() {
	sf.refreshMasterSink()
	sf.refreshMasterSource()
}

func (sf *paSessionFinder) refreshMasterSink() {
	sf.mu.RLock()
	client := sf.client
	sf.mu.RUnlock()
	if client == nil {
		return
	}

	reply := proto.GetSinkInfoReply{}
	if err := client.Request(&proto.GetSinkInfo{SinkIndex: proto.Undefined}, &reply); err != nil {
		sf.logger.Warnw("Failed to get master sink info", "error", err)
		return
	}

	sf.mu.Lock()
	old := sf.masterSink
	sf.masterSink = newMasterSession(sf.sessionLogger, sf.client, reply.SinkIndex, reply.Channels, true)
	sf.mu.Unlock()

	if old != nil {
		sf.emitEvent(SessionEvent{Type: SessionEventRemoved, Session: old})
		old.Release()
	}
	sf.emitEvent(SessionEvent{Type: SessionEventAdded, Session: sf.masterSink})
}

func (sf *paSessionFinder) refreshMasterSource() {
	sf.mu.RLock()
	client := sf.client
	sf.mu.RUnlock()
	if client == nil {
		return
	}

	reply := proto.GetSourceInfoReply{}
	if err := client.Request(&proto.GetSourceInfo{SourceIndex: proto.Undefined}, &reply); err != nil {
		sf.logger.Warnw("Failed to get master source info", "error", err)
		return
	}

	sf.mu.Lock()
	old := sf.masterSource
	sf.masterSource = newMasterSession(sf.sessionLogger, sf.client, reply.SourceIndex, reply.Channels, false)
	sf.mu.Unlock()

	if old != nil {
		sf.emitEvent(SessionEvent{Type: SessionEventRemoved, Session: old})
		old.Release()
	}
	sf.emitEvent(SessionEvent{Type: SessionEventAdded, Session: sf.masterSource})
}

func (sf *paSessionFinder) enumerateExistingSessions() {
	sf.mu.RLock()
	client := sf.client
	sf.mu.RUnlock()
	if client == nil {
		return
	}

	reply := proto.GetSinkInputInfoListReply{}
	if err := client.Request(&proto.GetSinkInputInfoList{}, &reply); err != nil {
		sf.logger.Warnw("Failed to enumerate sessions", "error", err)
		return
	}

	for _, info := range reply {
		sf.addSinkInputFromInfo(info)
	}
	sf.logger.Debugw("Enumerated sessions", "count", len(reply))
}

func (sf *paSessionFinder) addSinkInput(index uint32) {
	sf.mu.RLock()
	client := sf.client
	sf.mu.RUnlock()
	if client == nil {
		return
	}

	reply := proto.GetSinkInputInfoReply{}
	if err := client.Request(&proto.GetSinkInputInfo{SinkInputIndex: index}, &reply); err != nil {
		sf.logger.Debugw("Failed to get sink input info", "index", index, "error", err)
		return
	}
	sf.addSinkInputFromInfo(&reply)
}

func (sf *paSessionFinder) addSinkInputFromInfo(info *proto.GetSinkInputInfoReply) {
	// Try application.process.binary, then application.id, then application.name
	name, ok := info.Properties["application.process.binary"]
	if !ok {
		name, ok = info.Properties["application.id"]
		if !ok {
			name, ok = info.Properties["application.name"]
			if !ok {
				return
			}
		}
	}

	sf.mu.Lock()
	if _, exists := sf.sinkInputs[info.SinkInputIndex]; exists {
		sf.mu.Unlock()
		return
	}
	session := newPASession(sf.sessionLogger, sf.client, info.SinkInputIndex, info.Channels, name.String())
	sf.sinkInputs[info.SinkInputIndex] = session
	sf.mu.Unlock()

	sf.emitEvent(SessionEvent{Type: SessionEventAdded, Session: session})
	sf.logger.Debugw("Added session", "index", info.SinkInputIndex, "name", name.String())
}

func (sf *paSessionFinder) removeSinkInput(index uint32) {
	sf.mu.Lock()
	session, exists := sf.sinkInputs[index]
	if !exists {
		sf.mu.Unlock()
		return
	}
	delete(sf.sinkInputs, index)
	sf.mu.Unlock()

	sf.emitEvent(SessionEvent{Type: SessionEventRemoved, Session: session})
	session.Release()
	sf.logger.Debugw("Removed session", "index", index)
}

func (sf *paSessionFinder) emitEvent(event SessionEvent) {
	select {
	case sf.sessionEvents <- event:
	default:
	}
}

func (sf *paSessionFinder) GetAllSessions() ([]Session, error) {
	sf.mu.RLock()
	defer sf.mu.RUnlock()

	sessions := make([]Session, 0, 2+len(sf.sinkInputs))
	if sf.masterSink != nil {
		sessions = append(sessions, sf.masterSink)
	}
	if sf.masterSource != nil {
		sessions = append(sessions, sf.masterSource)
	}
	for _, s := range sf.sinkInputs {
		sessions = append(sessions, s)
	}
	return sessions, nil
}

func (sf *paSessionFinder) SubscribeToSessionEvents() <-chan SessionEvent {
	return sf.sessionEvents
}

func (sf *paSessionFinder) Release() error {
	sf.mu.Lock()
	sf.closed = true
	conn := sf.conn
	sf.mu.Unlock()

	if conn != nil {
		if err := conn.Close(); err != nil {
			return fmt.Errorf("close PulseAudio connection: %w", err)
		}
	}
	sf.logger.Debug("Released PA session finder")
	return nil
}
