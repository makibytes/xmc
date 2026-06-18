//go:build azmc

package azuresb

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus"
	"github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus/admin"

	"github.com/makibytes/xmc/broker/backends"
)

type ephemeralSub struct {
	topic string
	sub   string
}

type TopicAdapter struct {
	client       *azservicebus.Client
	adm          *admin.Client
	senders      map[string]*azservicebus.Sender
	ephemeralSubs []ephemeralSub
}

func NewTopicAdapter(args ConnArguments) (*TopicAdapter, error) {
	client, err := Connect(args)
	if err != nil {
		return nil, err
	}
	adm, err := AdminClient(args)
	if err != nil {
		client.Close(context.Background()) //nolint:errcheck
		return nil, err
	}
	return &TopicAdapter{
		client:  client,
		adm:     adm,
		senders: make(map[string]*azservicebus.Sender),
	}, nil
}

func (a *TopicAdapter) Publish(ctx context.Context, opts backends.PublishOptions) error {
	if err := a.ensureTopic(ctx, opts.Topic); err != nil {
		return err
	}

	sender, err := a.getSender(opts.Topic)
	if err != nil {
		return err
	}

	msg := toSBMessage(opts.Message, opts.Properties,
		opts.MessageID, opts.CorrelationID, opts.ReplyTo, opts.ContentType, opts.TTL)

	return sender.SendMessage(ctx, msg, nil)
}

func (a *TopicAdapter) Subscribe(ctx context.Context, opts backends.SubscribeOptions) (*backends.Message, error) {
	var subName string
	ephemeral := false
	if opts.GroupID != "" {
		subName = opts.GroupID
	} else if opts.Durable {
		subName = fmt.Sprintf("xmc-durable-%s", opts.Topic)
	} else {
		subName = fmt.Sprintf("xmc-sub-%s", randomSuffix())
		ephemeral = true
	}

	if err := ensureTopicAndSub(ctx, a.adm, opts.Topic, subName); err != nil {
		return nil, err
	}
	if ephemeral {
		a.ephemeralSubs = append(a.ephemeralSubs, ephemeralSub{topic: opts.Topic, sub: subName})
	}

	recv, err := a.client.NewReceiverForSubscription(opts.Topic, subName, nil)
	if err != nil {
		return nil, fmt.Errorf("creating subscription receiver %s/%s: %w", opts.Topic, subName, err)
	}
	defer recv.Close(ctx) //nolint:errcheck

	timeout := backends.TimeoutDuration(opts.Timeout, opts.Wait)
	receiveCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	msgs, err := recv.ReceiveMessages(receiveCtx, 1, nil)
	if err != nil {
		if receiveCtx.Err() != nil {
			return nil, backends.ErrNoMessageAvailable
		}
		return nil, fmt.Errorf("receiving from subscription %s/%s: %w", opts.Topic, subName, err)
	}
	if len(msgs) == 0 {
		return nil, backends.ErrNoMessageAvailable
	}

	if err := recv.CompleteMessage(ctx, msgs[0], nil); err != nil {
		return nil, fmt.Errorf("acknowledging message: %w", err)
	}

	return sbToBackendMessage(msgs[0]), nil
}

func (a *TopicAdapter) Close() error {
	ctx := context.Background()

	for _, es := range a.ephemeralSubs {
		a.adm.DeleteSubscription(ctx, es.topic, es.sub, nil) //nolint:errcheck
	}

	for _, s := range a.senders {
		s.Close(ctx) //nolint:errcheck
	}

	return a.client.Close(ctx)
}

func (a *TopicAdapter) ensureTopic(ctx context.Context, topic string) error {
	_, err := a.adm.GetTopic(ctx, topic, nil)
	if err == nil {
		return nil
	}
	_, err = a.adm.CreateTopic(ctx, topic, nil)
	if err != nil {
		return fmt.Errorf("creating topic %s: %w", topic, err)
	}
	return nil
}

func (a *TopicAdapter) getSender(topic string) (*azservicebus.Sender, error) {
	if s, ok := a.senders[topic]; ok {
		return s, nil
	}
	s, err := a.client.NewSender(topic, nil)
	if err != nil {
		return nil, fmt.Errorf("creating sender for topic %s: %w", topic, err)
	}
	a.senders[topic] = s
	return s, nil
}

func randomSuffix() string {
	var b [6]byte
	rand.Read(b[:]) //nolint:errcheck
	return hex.EncodeToString(b[:])
}
