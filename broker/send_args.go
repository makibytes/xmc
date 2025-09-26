//go:build artemis

package broker

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
