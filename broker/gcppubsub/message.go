//go:build google

package gcppubsub

import (
	"cloud.google.com/go/pubsub"
	"github.com/makibytes/xmc/broker/backends"
)

func buildAttributes(props map[string]any, messageID, correlationID, replyTo, contentType string) map[string]string {
	attrs := backends.StringifyProps(props)
	if messageID != "" {
		attrs[backends.PropMessageID] = messageID
	}
	if correlationID != "" {
		attrs[backends.PropCorrelationID] = correlationID
	}
	if replyTo != "" {
		attrs[backends.PropReplyTo] = replyTo
	}
	if contentType != "" {
		attrs[backends.PropContentType] = contentType
	}
	return attrs
}

func pubsubToBackendMessage(msg *pubsub.Message) *backends.Message {
	attrs := msg.Attributes
	props := make(map[string]any)
	for k, v := range attrs {
		switch k {
		case backends.PropMessageID, backends.PropCorrelationID,
			backends.PropReplyTo, backends.PropContentType:
			continue
		default:
			props[k] = v
		}
	}

	result := &backends.Message{
		Data:          msg.Data,
		Properties:    props,
		MessageID:     attrs[backends.PropMessageID],
		CorrelationID: attrs[backends.PropCorrelationID],
		ReplyTo:       attrs[backends.PropReplyTo],
		ContentType:   attrs[backends.PropContentType],
		Key:           msg.OrderingKey,
		InternalMetadata: map[string]any{
			"pubsub-id":    msg.ID,
			"publish-time": msg.PublishTime.String(),
		},
	}
	// Back-fill with the server-assigned Pub/Sub ID when the sender set none.
	if result.MessageID == "" {
		result.MessageID = msg.ID
	}
	return result
}
