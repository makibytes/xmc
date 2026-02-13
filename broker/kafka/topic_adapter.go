//go:build kafka

package kafka

import (
	"context"
	"fmt"

	"github.com/makibytes/amc/broker/backends"
	"github.com/segmentio/kafka-go"
)

// TopicAdapter adapts Kafka to the TopicBackend interface
type TopicAdapter struct {
	connArgs ConnArguments
}

// NewTopicAdapter creates a new Kafka topic adapter
func NewTopicAdapter(connArgs ConnArguments) (*TopicAdapter, error) {
	return &TopicAdapter{
		connArgs: connArgs,
	}, nil
}

// Publish implements backends.TopicBackend
func (a *TopicAdapter) Publish(ctx context.Context, opts backends.PublishOptions) error {
	properties := make(map[string]string)
	for k, v := range opts.Properties {
		properties[k] = fmt.Sprintf("%v", v)
	}

	args := SendArguments{
		Topic:         opts.Topic,
		Message:       opts.Message,
		Key:           opts.Key,
		Properties:    properties,
		MessageID:     opts.MessageID,
		CorrelationID: opts.CorrelationID,
		ReplyTo:       opts.ReplyTo,
		ContentType:   opts.ContentType,
		TTL:           opts.TTL,
	}

	return PublishMessage(ctx, a.connArgs, args)
}

// Subscribe implements backends.TopicBackend
func (a *TopicAdapter) Subscribe(ctx context.Context, opts backends.SubscribeOptions) (*backends.Message, error) {
	args := ReceiveArguments{
		Topic:                     opts.Topic,
		GroupID:                   opts.GroupID,
		Timeout:                   opts.Timeout,
		Wait:                      opts.Wait,
		Number:                    1,
		WithHeaderAndProperties:   opts.WithHeaderAndProperties,
		WithApplicationProperties: opts.WithApplicationProperties,
	}

	message, err := SubscribeMessage(ctx, a.connArgs, args)
	if err != nil {
		return nil, err
	}
	if message == nil {
		return nil, fmt.Errorf("no message available")
	}

	return convertKafkaToBackendMessage(message, opts.WithHeaderAndProperties), nil
}

// Close implements backends.TopicBackend
func (a *TopicAdapter) Close() error {
	// Kafka connections are per-operation, no persistent connection to close
	return nil
}

func convertKafkaToBackendMessage(msg *kafka.Message, withMetadata bool) *backends.Message {
	result := &backends.Message{
		Data:             msg.Value,
		Properties:       make(map[string]any),
		InternalMetadata: make(map[string]any),
	}

	// Convert headers to properties
	for _, h := range msg.Headers {
		result.Properties[h.Key] = string(h.Value)
	}

	// Extract standard metadata from headers if present
	if contentType, ok := result.Properties["content-type"]; ok {
		result.ContentType = fmt.Sprintf("%v", contentType)
		delete(result.Properties, "content-type")
	}
	if correlID, ok := result.Properties["correlation-id"]; ok {
		result.CorrelationID = fmt.Sprintf("%v", correlID)
		delete(result.Properties, "correlation-id")
	}
	if msgID, ok := result.Properties["message-id"]; ok {
		result.MessageID = fmt.Sprintf("%v", msgID)
		delete(result.Properties, "message-id")
	}
	if replyTo, ok := result.Properties["reply-to"]; ok {
		result.ReplyTo = fmt.Sprintf("%v", replyTo)
		delete(result.Properties, "reply-to")
	}

	// Add internal metadata for verbose display
	if withMetadata {
		result.InternalMetadata["Topic"] = msg.Topic
		result.InternalMetadata["Partition"] = msg.Partition
		result.InternalMetadata["Offset"] = msg.Offset
		result.InternalMetadata["Time"] = msg.Time
		if msg.Key != nil && len(msg.Key) > 0 {
			result.InternalMetadata["Key"] = string(msg.Key)
		}
	}

	return result
}