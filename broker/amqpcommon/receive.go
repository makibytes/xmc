package amqpcommon

import (
	"context"
	"time"

	"github.com/Azure/go-amqp"
	"github.com/makibytes/amc/log"
)

// ReceiveOptions configures an AMQP receive operation
type ReceiveOptions struct {
	Queue              string
	Timeout            float32
	Wait               bool     // true = wait indefinitely for a message
	Acknowledge        bool     // true = accept (destructive), false = release (peek)
	Durable            bool
	SourceCapabilities []string // e.g. ["queue"] or ["topic"] for Artemis routing
	Selector           string   // JMS-style message selector (AMQP filter)
	DurableSubscription bool    // create a durable subscription
	SubscriptionName   string   // name for durable subscription
}

// ReceiveMessage receives a single message from an AMQP 1.0 session
func ReceiveMessage(session *amqp.Session, opts ReceiveOptions) (*amqp.Message, error) {
	var ctx context.Context
	var cancel context.CancelFunc
	if opts.Wait {
		ctx, cancel = context.WithCancel(context.Background())
	} else {
		ctx, cancel = context.WithTimeout(context.Background(), time.Duration(float64(opts.Timeout)*float64(time.Second)))
	}
	defer cancel()

	var durability amqp.Durability
	if opts.Durable || opts.DurableSubscription {
		durability = amqp.DurabilityUnsettledState
	} else {
		durability = amqp.DurabilityNone
	}

	expiryPolicy := amqp.ExpiryPolicyLinkDetach
	linkName := "amc"
	if opts.DurableSubscription {
		expiryPolicy = amqp.ExpiryPolicyNever
		if opts.SubscriptionName != "" {
			linkName = opts.SubscriptionName
		}
	}

	receiverOptions := &amqp.ReceiverOptions{
		SourceCapabilities: opts.SourceCapabilities,
		SourceExpiryPolicy: expiryPolicy,
		Durability:         durability,
		Name:               linkName,
		SourceDurability:   durability,
		SettlementMode:     amqp.ReceiverSettleModeFirst.Ptr(),
	}

	// Add JMS selector as AMQP source filter
	if opts.Selector != "" {
		log.Verbose("applying selector filter: %s", opts.Selector)
		receiverOptions.Filters = []amqp.LinkFilter{
			amqp.NewSelectorFilter(opts.Selector),
		}
	}

	log.Verbose("generating receiver for %s...", opts.Queue)
	receiver, err := session.NewReceiver(ctx, opts.Queue, receiverOptions)
	if err != nil {
		return nil, err
	}
	defer receiver.Close(ctx)

	log.Verbose("calling receive()...")
	message, err := receiver.Receive(ctx, nil)
	if err != nil {
		return nil, err
	}

	if opts.Acknowledge {
		receiver.AcceptMessage(ctx, message)
	} else {
		receiver.ReleaseMessage(ctx, message)
	}

	return message, nil
}
