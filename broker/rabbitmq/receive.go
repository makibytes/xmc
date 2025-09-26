//go:build rabbitmq

package rabbitmq

import (
	"context"
	"time"

	"github.com/Azure/go-amqp"
	"github.com/makibytes/amc/log"
)

func ReceiveMessage(session *amqp.Session, args ReceiveArguments) (*amqp.Message, error) {
	var ctx context.Context
	var cancel context.CancelFunc
	if args.Timeout == 0 {
		ctx, cancel = context.WithCancel(context.Background())
	} else {
		if args.Timeout < 1 {
			ctx, cancel = context.WithTimeout(context.Background(), time.Duration(args.Timeout*1000)*time.Millisecond)
		} else {
			ctx, cancel = context.WithTimeout(context.Background(), time.Duration(args.Timeout)*time.Second)
		}
	}
	defer cancel()

	var durability amqp.Durability
	if args.Durable {
		durability = amqp.DurabilityUnsettledState
	} else {
		durability = amqp.DurabilityNone
	}

	receiverOptions := &amqp.ReceiverOptions{
		SourceExpiryPolicy: amqp.ExpiryPolicyLinkDetach,
		Durability:         durability,
		Name:               "amc",
		SourceDurability:   durability,
		SettlementMode:     amqp.ReceiverSettleModeFirst.Ptr(),
	}

	log.Verbose("📥 generating receiver for queue %s...", args.Queue)
	receiver, err := session.NewReceiver(ctx, args.Queue, receiverOptions)
	if err != nil {
		return nil, err
	}
	defer receiver.Close(ctx)

	log.Verbose("📩 calling receive()...")
	message, err := receiver.Receive(ctx, nil)
	if err != nil {
		return nil, err
	}

	// get: Accept, peek: Release (message stays in queue)
	if args.Acknowledge {
		receiver.AcceptMessage(ctx, message)
	} else {
		receiver.ReleaseMessage(ctx, message)
	}

	return message, nil
}