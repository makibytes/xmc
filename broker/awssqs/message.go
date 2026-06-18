//go:build awsmc

package awssqs

import (
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	snstypes "github.com/aws/aws-sdk-go-v2/service/sns/types"

	"github.com/makibytes/xmc/broker/backends"
)

func sqsAttributes(props map[string]any, messageID, correlationID, replyTo, contentType string) map[string]sqstypes.MessageAttributeValue {
	attrs := make(map[string]sqstypes.MessageAttributeValue)
	for k, v := range backends.StringifyProps(props) {
		attrs[k] = sqstypes.MessageAttributeValue{
			DataType:    strPtr("String"),
			StringValue: &v,
		}
	}
	setAttr := func(key, val string) {
		if val != "" {
			attrs[key] = sqstypes.MessageAttributeValue{
				DataType:    strPtr("String"),
				StringValue: &val,
			}
		}
	}
	setAttr(backends.PropMessageID, messageID)
	setAttr(backends.PropCorrelationID, correlationID)
	setAttr(backends.PropReplyTo, replyTo)
	setAttr(backends.PropContentType, contentType)
	return attrs
}

func snsAttributes(props map[string]any, messageID, correlationID, replyTo, contentType string) map[string]snstypes.MessageAttributeValue {
	attrs := make(map[string]snstypes.MessageAttributeValue)
	for k, v := range backends.StringifyProps(props) {
		attrs[k] = snstypes.MessageAttributeValue{
			DataType:    strPtr("String"),
			StringValue: &v,
		}
	}
	setAttr := func(key, val string) {
		if val != "" {
			attrs[key] = snstypes.MessageAttributeValue{
				DataType:    strPtr("String"),
				StringValue: &val,
			}
		}
	}
	setAttr(backends.PropMessageID, messageID)
	setAttr(backends.PropCorrelationID, correlationID)
	setAttr(backends.PropReplyTo, replyTo)
	setAttr(backends.PropContentType, contentType)
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
