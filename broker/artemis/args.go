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
	Subject       string
	To            string
}

type ReceiveArguments struct {
	Acknowledge               bool
	Durable                   bool
	Multicast                 bool
	Number                    int
	Queue                     string
	Timeout                   float32
	Wait                      bool
	WithHeaderAndProperties   bool
	WithApplicationProperties bool
}
