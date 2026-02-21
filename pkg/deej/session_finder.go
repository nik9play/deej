package deej

// SessionFinder represents an entity that can find all current audio sessions
type SessionFinder interface {
	GetAllSessions() ([]Session, error)

	// SubscribeToSessionEvents returns a channel that emits session add/remove events
	SubscribeToSessionEvents() <-chan SessionEvent

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
