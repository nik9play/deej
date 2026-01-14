package deej

// SessionFinder represents an entity that can find all current audio sessions
type SessionFinder interface {
	GetAllSessions() ([]Session, error)

	Release() error
}

// SessionEvent represents a session add/remove event
type SessionEvent struct {
	Type      SessionEventType
	Session   Session
	SessionID string
}

// SessionEventType indicates whether a session was added or removed
type SessionEventType int

const (
	// SessionEventAdded indicates a new session was created
	SessionEventAdded SessionEventType = iota
	// SessionEventRemoved indicates a session was removed/disconnected
	SessionEventRemoved
)

// EventDrivenSessionFinder is an optional interface for session finders that support
// event-based session tracking instead of polling
type EventDrivenSessionFinder interface {
	SessionFinder

	// SubscribeToSessionEvents returns a channel that emits session add/remove events
	SubscribeToSessionEvents() <-chan SessionEvent
}
