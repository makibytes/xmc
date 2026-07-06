//go:build artemis

package artemis

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/Azure/go-amqp"

	"github.com/makibytes/xmc/broker/amqpcommon"
	"github.com/makibytes/xmc/log"
)

var nextLinkID atomic.Uint64

func SendMessage(ctx context.Context, session *amqp.Session, args SendArguments) error {
	log.Verbose("🏗️  constructing message...")
	message := amqpcommon.BuildMessage(amqpcommon.MessageArgs{
		Payload:       args.Message,
		ContentType:   args.ContentType,
		CorrelationID: args.CorrelationID,
		MessageID:     args.MessageID,
		ReplyTo:       args.ReplyTo,
		Priority:      args.Priority,
		Durable:       args.Durable,
		TTL:           args.TTL,
		Properties:    args.Properties,
	})
	if args.TTL > 0 {
		log.Verbose("setting TTL to %d ms", args.TTL)
	}

	// AMQP 1.0 doesn't know about ANYCAST/MULTICAST, it's an Artemis-specific feature
	var artemisRouting uint8
	var targetCapabilities []string
	if args.Multicast {
		log.Verbose("🤟 with MULTICAST routing")
		artemisRouting = TopicType
		targetCapabilities = append(targetCapabilities, "topic")
	} else {
		log.Verbose("👉 with ANYCAST routing")
		artemisRouting = QueueType
		targetCapabilities = append(targetCapabilities, "queue")
	}
	message.DeliveryAnnotations = amqp.Annotations{
		"x-opt-jms-dest": artemisRouting,
	}

	durability := amqpcommon.LinkDurability(args.Durable)
	senderOptions := &amqp.SenderOptions{
		Durability:         durability,
		TargetCapabilities: targetCapabilities,
		TargetDurability:   durability,
		Name:               fmt.Sprintf("amc-%d", nextLinkID.Add(1)),
	}

	log.Verbose("📤 generating sender...")
	sender, err := session.NewSender(ctx, args.Address, senderOptions)
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
	return sender.Send(ctx, message, nil)
}
