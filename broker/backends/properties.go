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
//
// POLICY (native-first): these four keys exist only for brokers whose wire
// protocol has NO dedicated slot for the corresponding Message field
// (MessageID/CorrelationID/ReplyTo/ContentType) — Kafka headers, Pulsar/NATS/
// Redis/Google/AWS message properties or attributes. A broker adapter for a
// protocol that DOES have a native slot (AMQP's Properties.MessageID/
// CorrelationID/ReplyTo/ContentType, IBM MQ's MQMD.MsgId/CorrelId/ReplyToQ,
// Azure Service Bus's MessageID/CorrelationID/ReplyTo/ContentType fields)
// MUST map to/from that native slot on Send/Receive and MUST NOT place the
// field in the application-property map under these keys — doing so would
// both duplicate the native slot and risk colliding with a user's own
// same-named application property. IBM MQ's ContentType is the one partial
// exception: MQMD itself has no content-type slot, so it rides as a message-
// handle property under PropContentType (see broker/ibmmq/send.go), exactly
// like the header-based brokers.
//
// See docs/BROKERS.md's "Metadata Field Mapping" table for the current
// per-broker native-slot-vs-PropX audit; keep it updated when adding a
// broker or a new canonical field.
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
