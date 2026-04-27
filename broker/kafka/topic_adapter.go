//go:build kafka

package kafka

import (
	"context"
	"fmt"
	"strconv"

	"github.com/makibytes/xmc/broker/backends"
	"github.com/makibytes/xmc/log"
	"github.com/segmentio/kafka-go"
)

const propTTL = "ttl"

// TopicAdapter adapts Kafka to the TopicBackend interface
type TopicAdapter struct {
	connArgs ConnArguments
	writer   *kafka.Writer
}

// NewTopicAdapter creates a new Kafka topic adapter
func NewTopicAdapter(connArgs ConnArguments) (*TopicAdapter, error) {
	brokers, tlsConfig, err := parseKafkaURL(connArgs.Server, connArgs.TLS)
	if err != nil {
		return nil, err
	}

	writer := kafka.NewWriter(kafka.WriterConfig{
		Brokers:  brokers,
		Balancer: &kafka.LeastBytes{},
		Dialer:   buildDialer(connArgs, tlsConfig),
	})
	writer.AllowAutoTopicCreation = true

	return &TopicAdapter{connArgs: connArgs, writer: writer}, nil
}

// Publish implements backends.TopicBackend
func (a *TopicAdapter) Publish(ctx context.Context, opts backends.PublishOptions) error {
	var headers []kafka.Header
	addHeader := func(key, value string) {
		if value != "" {
			headers = append(headers, kafka.Header{Key: key, Value: []byte(value)})
		}
	}
	addHeader(backends.PropContentType, opts.ContentType)
	addHeader(backends.PropCorrelationID, opts.CorrelationID)
	addHeader(backends.PropMessageID, opts.MessageID)
	addHeader(backends.PropReplyTo, opts.ReplyTo)
	if opts.TTL > 0 {
		addHeader(propTTL, strconv.FormatInt(opts.TTL, 10))
	}
	for k, v := range backends.StringifyProps(opts.Properties) {
		addHeader(k, v)
	}

	message := kafka.Message{
		Topic:   opts.Topic,
		Key:     []byte(opts.Key),
		Value:   opts.Message,
		Headers: headers,
	}

	log.Verbose("💌 publishing message to topic %s...", opts.Topic)
	if err := a.writer.WriteMessages(ctx, message); err != nil {
		return fmt.Errorf("failed to publish message: %w", err)
	}
	return nil
}

// Subscribe implements backends.TopicBackend
func (a *TopicAdapter) Subscribe(ctx context.Context, opts backends.SubscribeOptions) (*backends.Message, error) {
	args := ReceiveArguments{
		Topic:   opts.Topic,
		GroupID: opts.GroupID,
		Timeout: opts.Timeout,
		Wait:    opts.Wait,
		Number:  1,
	}

	message, err := SubscribeMessage(ctx, a.connArgs, args)
	if err != nil {
		return nil, err
	}
	if message == nil {
		return nil, backends.ErrNoMessageAvailable
	}

	return convertKafkaToBackendMessage(message, opts.Verbosity >= backends.VerbosityVerbose), nil
}

// Close implements backends.TopicBackend
func (a *TopicAdapter) Close() error {
	if a.writer != nil {
		return a.writer.Close()
	}
	return nil
}

func convertKafkaToBackendMessage(msg *kafka.Message, withMetadata bool) *backends.Message {
	result := &backends.Message{
		Data:             msg.Value,
		Properties:       make(map[string]any),
		InternalMetadata: make(map[string]any),
	}

	for _, h := range msg.Headers {
		result.Properties[h.Key] = string(h.Value)
	}

	extract := func(key string, target *string) {
		if v, ok := result.Properties[key]; ok {
			*target = v.(string)
			delete(result.Properties, key)
		}
	}
	extract(backends.PropContentType, &result.ContentType)
	extract(backends.PropCorrelationID, &result.CorrelationID)
	extract(backends.PropMessageID, &result.MessageID)
	extract(backends.PropReplyTo, &result.ReplyTo)

	if withMetadata {
		result.InternalMetadata["Topic"] = msg.Topic
		result.InternalMetadata["Partition"] = msg.Partition
		result.InternalMetadata["Offset"] = msg.Offset
		result.InternalMetadata["Time"] = msg.Time
		if len(msg.Key) > 0 {
			result.InternalMetadata["Key"] = string(msg.Key)
		}
	}

	return result
}
