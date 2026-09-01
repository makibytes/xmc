//go:build artemis

package artemis

import (
	"context"

	"github.com/Azure/go-amqp"

	"github.com/makibytes/xmc/broker/amqpcommon"
	"github.com/makibytes/xmc/log"
)

func SendMessage(ctx context.Context, session *amqp.Session, cache *amqpcommon.SenderCache, args SendArguments) error {
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

	log.Verbose("📤 sending message...")
	return cache.Send(ctx, session, amqpcommon.SendOptions{
		Address:             args.Address,
		TargetCapabilities:  targetCapabilities,
		Durable:             args.Durable,
		LinkPrefix:          "amc",
		DeliveryAnnotations: amqp.Annotations{"x-opt-jms-dest": artemisRouting},
	}, message)
}
