//go:build kafka

package kafka

import (
	"context"
	"fmt"

	"github.com/makibytes/xmc/log"
	"github.com/segmentio/kafka-go"
)

// PublishMessage sends a message to a Kafka topic
func PublishMessage(ctx context.Context, connArgs ConnArguments, args SendArguments) error {
	brokers, tlsConfig, err := parseKafkaURL(connArgs.Server, connArgs.TLS)
	if err != nil {
		return err
	}

	log.Verbose("📤 creating Kafka writer...")

	writerConfig := kafka.WriterConfig{
		Brokers:  brokers,
		Topic:    args.Topic,
		Balancer: &kafka.LeastBytes{},
	}

	// Configure TLS if needed
	if tlsConfig != nil {
		writerConfig.Dialer = &kafka.Dialer{
			TLS: tlsConfig,
		}
	}

	// Configure SASL if credentials provided
	if sasl := getSASLMechanism(connArgs.User, connArgs.Password); sasl != nil {
		if writerConfig.Dialer == nil {
			writerConfig.Dialer = &kafka.Dialer{}
		}
		writerConfig.Dialer.SASLMechanism = sasl
	}

	writer := kafka.NewWriter(writerConfig)
	defer writer.Close()

	// Build message headers from properties
	var headers []kafka.Header
	if args.ContentType != "" {
		headers = append(headers, kafka.Header{Key: "content-type", Value: []byte(args.ContentType)})
	}
	if args.CorrelationID != "" {
		headers = append(headers, kafka.Header{Key: "correlation-id", Value: []byte(args.CorrelationID)})
	}
	if args.MessageID != "" {
		headers = append(headers, kafka.Header{Key: "message-id", Value: []byte(args.MessageID)})
	}
	if args.ReplyTo != "" {
		headers = append(headers, kafka.Header{Key: "reply-to", Value: []byte(args.ReplyTo)})
	}
	if args.TTL > 0 {
		headers = append(headers, kafka.Header{Key: "ttl", Value: []byte(fmt.Sprintf("%d", args.TTL))})
	}
	for k, v := range args.Properties {
		headers = append(headers, kafka.Header{Key: k, Value: []byte(v)})
	}

	message := kafka.Message{
		Key:     []byte(args.Key),
		Value:   args.Message,
		Headers: headers,
	}

	log.Verbose("💌 publishing message to topic %s...", args.Topic)
	err = writer.WriteMessages(ctx, message)
	if err != nil {
		return fmt.Errorf("failed to publish message: %w", err)
	}

	return nil
}