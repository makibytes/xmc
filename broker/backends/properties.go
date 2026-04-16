package backends

import "fmt"

// Verbosity controls how much metadata the broker surfaces back from a
// Receive/Subscribe call. VerbosityVerbose also opts into InternalMetadata
// (e.g. Kafka partition/offset, IBM MQ MQMD fields) for display purposes.
type Verbosity int

// Ordering is load-bearing: callers gate with `verbosity >= VerbosityNormal`
// and `verbosity >= VerbosityVerbose`. Keep values monotonic.
const (
	VerbosityQuiet   Verbosity = iota // data only
	VerbosityNormal                   // data + application properties
	VerbosityVerbose                  // data + application properties + metadata
)

// Standard message metadata property names shared by header-based brokers.
// Each broker places these on its native wire format (Kafka headers,
// Pulsar message properties, etc.) — so the strings are the interchange
// contract between publishers and subscribers using xmc.
const (
	PropContentType   = "content-type"
	PropCorrelationID = "correlation-id"
	PropMessageID     = "message-id"
	PropReplyTo       = "reply-to"
)

// StringifyProps converts an any-valued property map to a string map for
// brokers whose wire format only accepts string values.
func StringifyProps(props map[string]any) map[string]string {
	result := make(map[string]string, len(props))
	for k, v := range props {
		result[k] = fmt.Sprintf("%v", v)
	}
	return result
}
