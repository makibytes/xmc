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
	TTL           int64 // Time-to-live in tenths of a second for MQMD.Expiry
}

type ReceiveArguments struct {
	Queue                     string
	Timeout                   float32
	Wait                      bool
	Number                    int
	Acknowledge               bool // get = true, peek = false
	Selector                  string
	WithApplicationProperties bool
	WithHeaderAndProperties   bool
}
