//go:build artemis

package artemis

import (
	"context"

	"github.com/Azure/go-amqp"
	"github.com/makibytes/xmc/broker/amqpcommon"
	"github.com/makibytes/xmc/log"
)

// ReceiveMessage receives a message from Artemis with routing-specific capabilities.
// The caller's ctx is honoured for cancellation (Ctrl-C / Esc).
func ReceiveMessage(ctx context.Context, session *amqp.Session, args ReceiveArguments) (*amqp.Message, error) {
	var sourceCapabilities []string
	if args.Multicast {
		sourceCapabilities = append(sourceCapabilities, "topic")
		log.Verbose("with MULTICAST routing")
	} else {
		sourceCapabilities = append(sourceCapabilities, "queue")
		log.Verbose("with ANYCAST routing")
	}

	return amqpcommon.ReceiveMessage(ctx, session, amqpcommon.ReceiveOptions{
		Queue:               args.Queue,
		Timeout:             args.Timeout,
		Wait:                args.Wait,
		Acknowledge:         args.Acknowledge,
		Durable:             args.Durable,
		SourceCapabilities:  sourceCapabilities,
		Selector:            args.Selector,
		DurableSubscription: args.DurableSubscription,
		SubscriptionName:    args.SubscriptionName,
	})
}
