//go:build aws

package awssqs

import (
	snstypes "github.com/aws/aws-sdk-go-v2/service/sns/types"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"github.com/makibytes/xmc/broker/backends"
)

// messageAttributes builds the unified string map of message metadata and
// application properties. Both SQS and SNS callers convert from this.
func messageAttributes(props map[string]any, messageID, correlationID, replyTo, contentType string) map[string]string {
	attrs := backends.StringifyProps(props)
	set := func(key, val string) {
		if val != "" {
			attrs[key] = val
		}
	}
	set(backends.PropMessageID, messageID)
	set(backends.PropCorrelationID, correlationID)
	set(backends.PropReplyTo, replyTo)
	set(backends.PropContentType, contentType)
	return attrs
}

func sqsAttributes(props map[string]any, messageID, correlationID, replyTo, contentType string) map[string]sqstypes.MessageAttributeValue {
	raw := messageAttributes(props, messageID, correlationID, replyTo, contentType)
	attrs := make(map[string]sqstypes.MessageAttributeValue, len(raw))
	for k, v := range raw {
		attrs[k] = sqstypes.MessageAttributeValue{
			DataType:    strPtr("String"),
			StringValue: strPtr(v),
		}
	}
	return attrs
}

func snsAttributes(props map[string]any, messageID, correlationID, replyTo, contentType string) map[string]snstypes.MessageAttributeValue {
	raw := messageAttributes(props, messageID, correlationID, replyTo, contentType)
	attrs := make(map[string]snstypes.MessageAttributeValue, len(raw))
	for k, v := range raw {
		attrs[k] = snstypes.MessageAttributeValue{
			DataType:    strPtr("String"),
			StringValue: strPtr(v),
		}
	}
	return attrs
}

func sqsToBackendMessage(msg sqstypes.Message) *backends.Message {
	result := &backends.Message{
		Data:             []byte(derefStr(msg.Body)),
		Properties:       make(map[string]any),
		InternalMetadata: make(map[string]any),
	}

	if msg.MessageId != nil {
		result.InternalMetadata["sqs-message-id"] = *msg.MessageId
	}
	if msg.ReceiptHandle != nil {
		result.InternalMetadata["receipt-handle"] = *msg.ReceiptHandle
	}

	for k, v := range msg.MessageAttributes {
		val := derefStr(v.StringValue)
		switch k {
		case backends.PropMessageID:
			result.MessageID = val
		case backends.PropCorrelationID:
			result.CorrelationID = val
		case backends.PropReplyTo:
			result.ReplyTo = val
		case backends.PropContentType:
			result.ContentType = val
		default:
			result.Properties[k] = val
		}
	}

	return result
}

func strPtr(s string) *string { return &s }

func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
