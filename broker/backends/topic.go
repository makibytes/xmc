package backends

import "context"

// PublishOptions contains options for publishing messages to a topic
type PublishOptions struct {
	Topic         string
	Message       []byte
	Key           string // For partitioning (Kafka)
	Properties    map[string]any
	MessageID     string
	CorrelationID string
	ContentType   string
}

// SubscribeOptions contains options for subscribing to messages from a topic
type SubscribeOptions struct {
	Topic                     string
	GroupID                   string // Consumer group (Kafka)
	Timeout                   float32
	Wait                      bool
	WithHeaderAndProperties   bool
	WithApplicationProperties bool
}

// TopicBackend defines the interface for topic-based messaging brokers
type TopicBackend interface {
	// Publish publishes a message to a topic
	Publish(ctx context.Context, opts PublishOptions) error

	// Subscribe subscribes and receives a message from a topic
	Subscribe(ctx context.Context, opts SubscribeOptions) (*Message, error)

	// Close closes the connection to the broker
	Close() error
}
