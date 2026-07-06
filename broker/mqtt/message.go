//go:build mqtt

package mqtt

import (
	"strings"

	"github.com/eclipse/paho.golang/paho"

	"github.com/makibytes/xmc/broker/backends"
)

// buildPublishProperties maps xmc metadata to the native MQTT 5 property
// slots: content type, correlation data, response topic, and message expiry.
// MQTT 5 has no message-id slot, so a sender-set ID rides as a user property
// under PropMessageID like on the other header-based brokers. Returns nil when
// nothing is set so the wire stays clean (mirrors amqpcommon.BuildMessage).
func buildPublishProperties(props map[string]any, messageID, correlationID, responseTopic, contentType string, ttlMS int64) *paho.PublishProperties {
	if len(props) == 0 && messageID == "" && correlationID == "" &&
		responseTopic == "" && contentType == "" && ttlMS <= 0 {
		return nil
	}

	p := &paho.PublishProperties{
		ContentType:   contentType,
		ResponseTopic: responseTopic,
	}
	if correlationID != "" {
		p.CorrelationData = []byte(correlationID)
	}
	if ttlMS > 0 {
		expiry := uint32((ttlMS + 999) / 1000) // MessageExpiry is in seconds; round up
		p.MessageExpiry = &expiry
	}
	if messageID != "" {
		p.User.Add(backends.PropMessageID, messageID)
	}
	for k, v := range backends.StringifyProps(props) {
		p.User.Add(k, v)
	}
	return p
}

// convertPublish converts a received MQTT 5 publish to a backends.Message.
// stripQueuePrefix converts a response topic in xmc's queue namespace
// ("queue/<name>") back to the bare queue name for the queue-side commands
// (receive/request/reply).
func convertPublish(msg *paho.Publish, stripQueuePrefix bool) *backends.Message {
	data := make([]byte, len(msg.Payload))
	copy(data, msg.Payload)
	result := &backends.Message{
		Data:       data,
		Properties: make(map[string]any),
	}

	p := msg.Properties
	if p == nil {
		return result
	}

	result.ContentType = p.ContentType
	result.CorrelationID = string(p.CorrelationData)
	replyTo := p.ResponseTopic
	if stripQueuePrefix {
		replyTo = strings.TrimPrefix(replyTo, queueTopicPrefix)
	}
	result.ReplyTo = replyTo

	for _, up := range p.User {
		if up.Key == backends.PropMessageID && result.MessageID == "" {
			result.MessageID = up.Value
			continue
		}
		result.Properties[up.Key] = up.Value
	}

	if p.MessageExpiry != nil {
		// Remaining lifetime in seconds, decremented by the broker while queued.
		result.InternalMetadata = map[string]any{"message-expiry": *p.MessageExpiry}
	}

	return result
}
