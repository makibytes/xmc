//go:build rabbitmq

package rabbitmq

import (
	"context"

	"github.com/Azure/go-amqp"
	"github.com/makibytes/xmc/broker/amqpcommon"
)

// ReceiveMessage receives a message from RabbitMQ (no routing capabilities
// needed), via cache's cached receiver link (see amqpcommon.ReceiverCache).
// The caller's ctx is honoured for cancellation (Ctrl-C / Esc).
func ReceiveMessage(ctx context.Context, session *amqp.Session, cache *amqpcommon.ReceiverCache, args ReceiveArguments) (*amqp.Message, error) {
	return cache.Receive(ctx, session, amqpcommon.ReceiveOptions{
		Queue:       args.Queue,
		Timeout:     args.Timeout,
		Wait:        args.Wait,
		Acknowledge: args.Acknowledge,
		Selector:    args.Selector,
	})
}
