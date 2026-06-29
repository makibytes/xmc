//go:build rabbitmq

package rabbitmq

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/Azure/go-amqp"
	"github.com/makibytes/xmc/log"
)

var nextLinkID atomic.Uint64

func SendMessage(ctx context.Context, session *amqp.Session, args SendArguments) error {
	log.Verbose("🏗️  constructing message...")
	message := amqp.NewMessage(args.Message)
	message.Header = &amqp.MessageHeader{
		Durable:  args.Durable,
		Priority: args.Priority,
	}
	if args.TTL > 0 {
		message.Header.TTL = time.Duration(args.TTL) * time.Millisecond
		log.Verbose("setting TTL to %d ms", args.TTL)
	}
	message.Properties = &amqp.MessageProperties{
		ContentType:   &args.ContentType,
		CorrelationID: &args.CorrelationID,
		MessageID:     &args.MessageID,
		ReplyTo:       &args.ReplyTo,
		Subject:       &args.Subject,
		To:            &args.To,
	}

	if len(args.Properties) > 0 {
		message.ApplicationProperties = args.Properties
	}

	var durability amqp.Durability
	if args.Durable {
		durability = amqp.DurabilityUnsettledState
	} else {
		durability = amqp.DurabilityNone
	}

	targetAddress := args.Queue
	log.Verbose("📤 sending to %s...", targetAddress)

	senderOptions := &amqp.SenderOptions{
		Durability:       durability,
		SourceAddress:    targetAddress,
		TargetDurability: durability,
		Name:             fmt.Sprintf("rmc-%d", nextLinkID.Add(1)),
	}

	log.Verbose("📤 generating sender...")
	sender, err := session.NewSender(ctx, targetAddress, senderOptions)
	if err != nil {
		return err
	}
	// Use a fresh context for the close so the DETACH handshake always completes,
	// even if the operation's own ctx was cancelled.
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = sender.Close(closeCtx)
	}()

	log.Verbose("💌 sending message...")
	err = sender.Send(ctx, message, nil)

	return err
}
