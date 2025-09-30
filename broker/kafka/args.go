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