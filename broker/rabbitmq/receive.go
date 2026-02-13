//go:build rabbitmq

package rabbitmq

import (
	"github.com/Azure/go-amqp"
	"github.com/makibytes/amc/broker/amqpcommon"
)

// ReceiveMessage receives a message from RabbitMQ (no routing capabilities needed)
func ReceiveMessage(session *amqp.Session, args ReceiveArguments) (*amqp.Message, error) {
	return amqpcommon.ReceiveMessage(session, amqpcommon.ReceiveOptions{
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
