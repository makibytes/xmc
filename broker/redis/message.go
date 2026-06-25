//go:build redis

package redis

import (
	"github.com/makibytes/xmc/broker/backends"
)

const (
	fieldData          = "data"
	fieldMessageID     = backends.PropMessageID
	fieldCorrelationID = backends.PropCorrelationID
	fieldReplyTo       = backends.PropReplyTo
	fieldContentType   = backends.PropContentType
	propPrefix         = "p:"
)

func buildFields(data []byte, props map[string]any, messageID, correlationID, replyTo, contentType string) map[string]any {
	fields := map[string]any{
		fieldData: data,
	}
	if messageID != "" {
		fields[fieldMessageID] = messageID
	}
	if correlationID != "" {
		fields[fieldCorrelationID] = correlationID
	}
	if replyTo != "" {
		fields[fieldReplyTo] = replyTo
	}
	if contentType != "" {
		fields[fieldContentType] = contentType
	}
	for k, v := range backends.StringifyProps(props) {
		fields[propPrefix+k] = v
	}
	return fields
}

var reservedFields = map[string]struct{}{
	fieldData:          {},
	fieldMessageID:     {},
	fieldCorrelationID: {},
	fieldReplyTo:       {},
	fieldContentType:   {},
}

func streamToMessage(id string, values map[string]any) *backends.Message {
	msg := &backends.Message{
		Properties:       make(map[string]any),
		InternalMetadata: map[string]any{"stream-id": id},
	}

	for k, v := range values {
		s, _ := v.(string)
		switch k {
		case fieldData:
			msg.Data = []byte(s)
		case fieldMessageID:
			msg.MessageID = s
		case fieldCorrelationID:
			msg.CorrelationID = s
		case fieldReplyTo:
			msg.ReplyTo = s
		case fieldContentType:
			msg.ContentType = s
		default:
			if len(k) > len(propPrefix) && k[:len(propPrefix)] == propPrefix {
				msg.Properties[k[len(propPrefix):]] = s
			}
		}
	}

	return msg
}
