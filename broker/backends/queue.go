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
	TTL           int64 // Time-to-live in milliseconds (0 = no expiry)
}

// ReceiveOptions contains options for receiving messages from a queue
type ReceiveOptions struct {
	Queue                     string
	Timeout                   float32
	Wait                      bool
	Acknowledge               bool // true = destructive read (get), false = browse (peek)
	WithHeaderAndProperties   bool
	WithApplicationProperties bool
	Selector                  string // JMS-style message selector expression
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

// ManageableBackend is an optional interface for brokers that support management operations
type ManageableBackend interface {
	// ListQueues lists all queues on the broker
	ListQueues(ctx context.Context) ([]QueueInfo, error)

	// PurgeQueue removes all messages from a queue
	PurgeQueue(ctx context.Context, queue string) (int64, error)

	// GetQueueStats returns statistics for a queue
	GetQueueStats(ctx context.Context, queue string) (*QueueStats, error)
}

// QueueInfo contains basic information about a queue
type QueueInfo struct {
	Name         string
	MessageCount int64
	ConsumerCount int
}

// QueueStats contains detailed statistics for a queue
type QueueStats struct {
	Name          string
	MessageCount  int64
	ConsumerCount int
	EnqueueCount  int64 // total messages enqueued (lifetime)
	DequeueCount  int64 // total messages dequeued (lifetime)
}
