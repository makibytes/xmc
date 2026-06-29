//go:build rabbitmq

package rabbitmq

import (
	"context"

	"github.com/Azure/go-amqp"
	"github.com/makibytes/xmc/broker/amqpcommon"
)

// ReceiveMessage receives a message from RabbitMQ (no routing capabilities needed).
// The caller's ctx is honoured for cancellation (Ctrl-C / Esc).
func ReceiveMessage(ctx context.Context, session *amqp.Session, args ReceiveArguments) (*amqp.Message, error) {
	return amqpcommon.ReceiveMessage(ctx, session, amqpcommon.ReceiveOptions{
		Queue:               args.Queue,
		Timeout:             args.Timeout,
		Wait:                args.Wait,
		Acknowledge:         args.Acknowledge,
		Durable:             args.Durable,
		Selector:            args.Selector,
		DurableSubscription: args.DurableSubscription,
		SubscriptionName:    args.SubscriptionName,
	})
}
