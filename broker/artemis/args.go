//go:build artemis

// Package artemis implements the Apache Artemis (AMQP 1.0) broker backend.
package artemis

// SendArguments holds the parameters for sending a message to Artemis.
type SendArguments struct {
	Address       string
	ContentType   string
	CorrelationID string
	Durable       bool
	Message       []byte
	MessageID     string
	Multicast     bool
	Priority      uint8
	Properties    map[string]any
	ReplyTo       string
	TTL           int64 // Time-to-live in milliseconds
}

// ReceiveArguments holds the parameters for receiving a message from Artemis.
type ReceiveArguments struct {
	Acknowledge         bool
	DurableSubscription bool
	Multicast           bool
	Queue               string
	Selector            string
	SubscriptionName    string
	Timeout             float32
	Wait                bool
}
