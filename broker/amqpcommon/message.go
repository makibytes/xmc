package amqpcommon

import (
	"fmt"
	"time"

	"github.com/Azure/go-amqp"
	"github.com/makibytes/xmc/broker/backends"
)

// MessageArgs collects the broker-neutral fields for constructing an AMQP 1.0
// message. Broker-specific extras (e.g. Artemis routing annotations) are added
// by the caller on the returned message.
type MessageArgs struct {
	Payload       []byte
	ContentType   string
	CorrelationID string
	MessageID     string
	ReplyTo       string
	Priority      uint8
	Durable       bool
	TTL           int64 // milliseconds, 0 = no expiry
	Properties    map[string]any
}

// BuildMessage constructs an AMQP 1.0 message, mapping the metadata fields to
// their native slots in the properties section (native-first policy, see
// backends/properties.go). Unset fields are omitted from the wire entirely
// instead of being transmitted as empty strings.
func BuildMessage(args MessageArgs) *amqp.Message {
	message := amqp.NewMessage(args.Payload)
	message.Header = &amqp.MessageHeader{
		Durable:  args.Durable,
		Priority: args.Priority,
	}
	if args.TTL > 0 {
		message.Header.TTL = time.Duration(args.TTL) * time.Millisecond
	}

	props := &amqp.MessageProperties{}
	hasProps := false
	if args.MessageID != "" {
		props.MessageID = args.MessageID
		hasProps = true
	}
	if args.CorrelationID != "" {
		props.CorrelationID = args.CorrelationID
		hasProps = true
	}
	if args.ReplyTo != "" {
		props.ReplyTo = &args.ReplyTo
		hasProps = true
	}
	if args.ContentType != "" {
		props.ContentType = &args.ContentType
		hasProps = true
	}
	if hasProps {
		message.Properties = props
	}

	if len(args.Properties) > 0 {
		message.ApplicationProperties = args.Properties
	}

	return message
}

// LinkDurability maps the persistent/durable flag to the AMQP link durability
// used for sender and receiver links.
func LinkDurability(durable bool) amqp.Durability {
	if durable {
		return amqp.DurabilityUnsettledState
	}
	return amqp.DurabilityNone
}

// ConvertAMQPToBackendMessage converts an AMQP 1.0 message to the common backend Message type.
// withMetadata controls whether AMQP header/properties debug strings are surfaced via InternalMetadata.
func ConvertAMQPToBackendMessage(msg *amqp.Message, withMetadata bool) *backends.Message {
	result := &backends.Message{
		Data:       msg.GetData(),
		Properties: msg.ApplicationProperties,
	}
	if withMetadata {
		result.InternalMetadata = make(map[string]any)
	}

	if msg.Properties != nil {
		if msg.Properties.MessageID != nil {
			result.MessageID = fmt.Sprintf("%v", msg.Properties.MessageID)
		}
		if msg.Properties.CorrelationID != nil {
			result.CorrelationID = fmt.Sprintf("%v", msg.Properties.CorrelationID)
		}
		if msg.Properties.ReplyTo != nil {
			result.ReplyTo = *msg.Properties.ReplyTo
		}
		if msg.Properties.ContentType != nil {
			result.ContentType = *msg.Properties.ContentType
		}
		if withMetadata {
			result.InternalMetadata["MessageProperties"] = fmt.Sprintf("%+v", msg.Properties)
		}
	}

	if msg.Header != nil {
		result.Priority = int(msg.Header.Priority)
		result.Persistent = msg.Header.Durable
		if withMetadata {
			result.InternalMetadata["Header"] = fmt.Sprintf("%+v", msg.Header)
		}
	}

	return result
}
