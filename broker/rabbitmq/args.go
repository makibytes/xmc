//go:build rabbitmq

// Package rabbitmq implements the RabbitMQ (AMQP 1.0) broker backend.
package rabbitmq

// SendArguments holds the parameters for sending a message to RabbitMQ.
type SendArguments struct {
	Queue         string
	Message       []byte
	ContentType   string
	CorrelationID string
	MessageID     string
	Priority      uint8
	Durable       bool
	Properties    map[string]any
	ReplyTo       string
	TTL           int64 // Time-to-live in milliseconds
}

// ReceiveArguments holds the parameters for receiving a message from RabbitMQ.
type ReceiveArguments struct {
	Queue       string
	Acknowledge bool
	Selector    string
	Timeout     float32
	Wait        bool
}
