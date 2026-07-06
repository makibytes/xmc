//go:build azure

package azuresb

import (
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus"

	"github.com/makibytes/xmc/broker/backends"
)

func toSBMessage(data []byte, props map[string]any, messageID, correlationID, replyTo, contentType string, ttl int64) *azservicebus.Message {
	msg := &azservicebus.Message{
		Body: data,
	}

	if messageID != "" {
		msg.MessageID = &messageID
	}
	if correlationID != "" {
		msg.CorrelationID = &correlationID
	}
	if replyTo != "" {
		msg.ReplyTo = &replyTo
	}
	if contentType != "" {
		msg.ContentType = &contentType
	}
	if ttl > 0 {
		d := time.Duration(ttl) * time.Millisecond
		msg.TimeToLive = &d
	}

	// Service Bus speaks AMQP 1.0: application properties are typed on the
	// wire, so pass them through as-is like the other AMQP brokers.
	if len(props) > 0 {
		msg.ApplicationProperties = props
	}

	return msg
}

func sbToBackendMessage(msg *azservicebus.ReceivedMessage) *backends.Message {
	result := &backends.Message{
		Data:             msg.Body,
		Properties:       make(map[string]any),
		InternalMetadata: make(map[string]any),
	}

	if msg.MessageID != "" {
		result.MessageID = msg.MessageID
	}
	if msg.CorrelationID != nil {
		result.CorrelationID = *msg.CorrelationID
	}
	if msg.ReplyTo != nil {
		result.ReplyTo = *msg.ReplyTo
	}
	if msg.ContentType != nil {
		result.ContentType = *msg.ContentType
	}

	for k, v := range msg.ApplicationProperties {
		result.Properties[k] = v
	}

	if msg.SequenceNumber != nil {
		result.InternalMetadata["sequence-number"] = *msg.SequenceNumber
	}
	if msg.EnqueuedTime != nil {
		result.InternalMetadata["enqueued-time"] = msg.EnqueuedTime.String()
	}

	return result
}
