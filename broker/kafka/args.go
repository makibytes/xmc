//go:build kafka

// Package kafka implements the Apache Kafka broker backend.
package kafka

// OffsetUnset marks "no --offset given" in ReceiveArguments.Offset. It must be
// distinct from kafka-go's sentinel offsets (FirstOffset = -2, LastOffset = -1),
// which are valid values for --offset earliest / --offset latest.
const OffsetUnset int64 = -3

// ReceiveArguments holds the parameters for receiving a message from Kafka.
type ReceiveArguments struct {
	Topic     string
	Timeout   float32
	Wait      bool
	GroupID   string
	Partition int   // -1 = not set (use group/default)
	Offset    int64 // OffsetUnset, kafka.FirstOffset, kafka.LastOffset, or explicit
}
