//go:build rabbitmq

package rabbitmq

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

type ReceiveArguments struct {
	Queue       string
	Acknowledge bool
	Selector    string
	Timeout     float32
	Wait        bool
}
