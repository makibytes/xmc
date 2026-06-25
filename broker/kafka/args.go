//go:build kafka

package kafka

type ReceiveArguments struct {
	Topic     string
	Timeout   float32
	Wait      bool
	Number    int
	GroupID   string
	Partition int   // -1 = not set (use group/default)
	Offset    int64 // -1 = not set; kafka.FirstOffset / kafka.LastOffset / explicit
}
