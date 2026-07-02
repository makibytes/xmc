package backends

import "context"

// PublishOptions contains options for publishing messages to a topic
type PublishOptions struct {
	Topic         string
	Message       []byte
	Key           string // For partitioning (Kafka, Pulsar)
	Properties    map[string]any
	MessageID     string
	CorrelationID string
	ReplyTo       string
	ContentType   string
	Priority      int
	Persistent    bool
	TTL           int64             // Time-to-live in milliseconds (0 = no expiry)
	Extra         map[string]string // Broker-specific flags (e.g. qos, retain, routing-type)
}

// SubscribeOptions contains options for subscribing to messages from a topic
type SubscribeOptions struct {
	Topic       string
	GroupID     string // Consumer group (Kafka)
	Timeout     float32
	Wait        bool
	Verbosity   Verbosity
	Selector    string            // JMS-style message selector expression
	Durable     bool              // Create a durable subscription
	Acknowledge bool              // Consume the message (true) vs. leave it for redelivery (false, non-destructive peek); honored by Azure/Google, ignored elsewhere (they always ack)
	Extra       map[string]string // Broker-specific flags (e.g. subscription, partition, offset)
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

// TopicInfo contains basic information about a topic
type TopicInfo struct {
	Name           string
	PartitionCount int // Kafka-specific
	ConsumerGroups int
}
