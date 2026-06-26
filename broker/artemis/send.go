//go:build artemis

package artemis

import (
	"context"
	"time"

	"github.com/Azure/go-amqp"

	"github.com/makibytes/xmc/log"
)

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

	//TODO: reply queue -> x-opt-jms-reply-to

	if len(args.Properties) > 0 {
		message.ApplicationProperties = args.Properties
	}

	var durability amqp.Durability
	if args.Durable {
		durability = amqp.DurabilityUnsettledState
	} else {
		durability = amqp.DurabilityNone
	}

	senderOptions := &amqp.SenderOptions{
		Durability: durability,
		//		DynamicAddress:   true,
		SourceAddress:      args.Address,
		TargetCapabilities: targetCapabilities,
		TargetDurability:   durability,
		Name:               "amc",
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
	err = sender.Send(ctx, message, nil)

	return err
}
