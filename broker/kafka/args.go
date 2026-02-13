//go:build kafka

package kafka

type SendArguments struct {
	Topic         string
	Message       []byte
	Key           string
	Properties    map[string]string
	ContentType   string
	CorrelationID string
	MessageID     string
	ReplyTo       string
	TTL           int64 // Time-to-live in milliseconds (stored as header)
}

type ReceiveArguments struct {
	Topic                     string
	Timeout                   float32
	Wait                      bool
	Number                    int
	GroupID                   string
	WithApplicationProperties bool
	WithHeaderAndProperties   bool
}