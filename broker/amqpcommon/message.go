package amqpcommon

import (
	"fmt"

	"github.com/Azure/go-amqp"
	"github.com/makibytes/amc/broker/backends"
	"github.com/makibytes/amc/log"
)

// ConvertAMQPToBackendMessage converts an AMQP 1.0 message to the common backend Message type
func ConvertAMQPToBackendMessage(msg *amqp.Message) *backends.Message {
	result := &backends.Message{
		Data:             msg.GetData(),
		Properties:       msg.ApplicationProperties,
		InternalMetadata: make(map[string]any),
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

		if log.IsVerbose {
			result.InternalMetadata["Header"] = fmt.Sprintf("%+v", msg.Header)
			result.InternalMetadata["MessageProperties"] = fmt.Sprintf("%+v", msg.Properties)
		}
	}

	if msg.Header != nil {
		result.Priority = int(msg.Header.Priority)
		result.Persistent = msg.Header.Durable
	}

	return result
}
