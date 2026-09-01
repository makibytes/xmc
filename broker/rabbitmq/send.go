//go:build rabbitmq

package rabbitmq

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

	log.Verbose("📤 sending message to %s...", args.Queue)
	return cache.Send(ctx, session, amqpcommon.SendOptions{
		Address:    args.Queue,
		Durable:    args.Durable,
		LinkPrefix: "rmc",
	}, message)
}
