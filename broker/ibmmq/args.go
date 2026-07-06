//go:build ibmmq

package ibmmq

type SendArguments struct {
	Queue         string
	Message       []byte
	Properties    map[string]string
	ContentType   string
	CorrelationID string
	MessageID     string
	Priority      int
	Persistence   int
	ReplyTo       string
	TTL           int64 // Time-to-live in milliseconds (converted to MQMD.Expiry tenths of a second)
}

type ReceiveArguments struct {
	Queue       string
	Timeout     float32
	Wait        bool
	Acknowledge bool // get = true, peek = false
	Selector    string
}
