//go:build kafka

package kafka

type ReceiveArguments struct {
	Topic   string
	Timeout float32
	Wait    bool
	Number  int
	GroupID string
}
