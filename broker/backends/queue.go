package backends

import "context"

// Message represents a generic message for queue operations
type Message struct {
	Data       []byte
	Properties map[string]any

	// Message metadata
	MessageID     string
	CorrelationID string
	ReplyTo       string
	ContentType   string
	Priority      int
	Persistent    bool

	// Internal metadata (for display purposes)
	InternalMetadata map[string]any
}

// SendOptions contains options for sending messages to a queue
type SendOptions struct {
	Queue         string
	Message       []byte
	Properties    map[string]any
	MessageID     string
	CorrelationID string
	ReplyTo       string
	ContentType   string
	Priority      int
	Persistent    bool
}

// ReceiveOptions contains options for receiving messages from a queue
type ReceiveOptions struct {
	Queue                     string
	Timeout                   float32
	Wait                      bool
	Acknowledge               bool // true = destructive read (get), false = browse (peek)
	WithHeaderAndProperties   bool
	WithApplicationProperties bool
}

// QueueBackend defines the interface for queue-based messaging brokers
type QueueBackend interface {
	// Send sends a message to a queue
	Send(ctx context.Context, opts SendOptions) error

	// Receive receives a message from a queue
	Receive(ctx context.Context, opts ReceiveOptions) (*Message, error)

	// Close closes the connection to the broker
	Close() error
}
