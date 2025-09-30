//go:build rabbitmq

package rabbitmq

type SendArguments struct {
	Queue         string
	Exchange      string // For topic mode: specify exchange name
	RoutingKey    string // For topic mode: routing key
	Message       []byte
	ContentType   string
	CorrelationID string
	MessageID     string
	Priority      uint8
	Durable       bool
	Properties    map[string]any
	ReplyTo       string
	Subject       string
	To            string
}

type ReceiveArguments struct {
	Queue                     string
	Acknowledge               bool
	Durable                   bool
	Number                    int
	Timeout                   float32
	Wait                      bool
	WithHeaderAndProperties   bool
	WithApplicationProperties bool
}