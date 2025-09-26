//go:build kafka

package kafka

import (
	"context"
	"fmt"
	"time"

	"github.com/makibytes/amc/log"
	"github.com/segmentio/kafka-go"
)

// SubscribeMessage receives messages from a Kafka topic
func SubscribeMessage(ctx context.Context, connArgs ConnArguments, args ReceiveArguments) (*kafka.Message, error) {
	brokers, tlsConfig, err := parseKafkaURL(connArgs.Server)
	if err != nil {
		return nil, err
	}

	// Apply timeout if specified
	if args.Timeout > 0 && !args.Wait {
		var cancel context.CancelFunc
		if args.Timeout < 1 {
			ctx, cancel = context.WithTimeout(ctx, time.Duration(args.Timeout*1000)*time.Millisecond)
		} else {
			ctx, cancel = context.WithTimeout(ctx, time.Duration(args.Timeout)*time.Second)
		}
		defer cancel()
	}

	log.Verbose("📥 creating Kafka reader...")

	readerConfig := kafka.ReaderConfig{
		Brokers:  brokers,
		Topic:    args.Topic,
		GroupID:  args.GroupID,
		MinBytes: 10e3, // 10KB
		MaxBytes: 10e6, // 10MB
	}

	// Configure TLS if needed
	if tlsConfig != nil {
		readerConfig.Dialer = &kafka.Dialer{
			TLS: tlsConfig,
		}
	}

	// Configure SASL if credentials provided
	if sasl := getSASLMechanism(connArgs.User, connArgs.Password); sasl != nil {
		if readerConfig.Dialer == nil {
			readerConfig.Dialer = &kafka.Dialer{}
		}
		readerConfig.Dialer.SASLMechanism = sasl
	}

	reader := kafka.NewReader(readerConfig)
	defer reader.Close()

	log.Verbose("📩 subscribing to topic %s...", args.Topic)
	message, err := reader.FetchMessage(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch message: %w", err)
	}

	// Commit the message (acknowledge)
	if err := reader.CommitMessages(ctx, message); err != nil {
		log.Verbose("⚠️  failed to commit message: %v", err)
	}

	return &message, nil
}