//go:build artemis

package artemis

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
