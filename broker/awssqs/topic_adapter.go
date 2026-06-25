//go:build aws

package awssqs

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"github.com/makibytes/xmc/broker/backends"
)

type TopicAdapter struct {
	sqsc            *sqs.Client
	snsc            *sns.Client
	ephemeralQueues []string
	subscriptions   []string
}

func NewTopicAdapter(args ConnArguments) (*TopicAdapter, error) {
	sqsc, snsc, err := Connect(context.Background(), args)
	if err != nil {
		return nil, err
	}
	return &TopicAdapter{sqsc: sqsc, snsc: snsc}, nil
}

func (a *TopicAdapter) Publish(ctx context.Context, opts backends.PublishOptions) error {
	topicARN, err := ensureTopic(ctx, a.snsc, opts.Topic)
	if err != nil {
		return err
	}

	body := string(opts.Message)
	attrs := snsAttributes(opts.Properties,
		opts.MessageID, opts.CorrelationID, opts.ReplyTo, opts.ContentType)

	input := &sns.PublishInput{
		TopicArn:          &topicARN,
		Message:           &body,
		MessageAttributes: attrs,
	}

	if gid := opts.Extra["message-group-id"]; gid != "" {
		input.MessageGroupId = &gid
	}
	if did := opts.Extra["dedup-id"]; did != "" {
		input.MessageDeduplicationId = &did
	}

	_, err = a.snsc.Publish(ctx, input)
	return err
}

func (a *TopicAdapter) Subscribe(ctx context.Context, opts backends.SubscribeOptions) (*backends.Message, error) {
	topicARN, err := ensureTopic(ctx, a.snsc, opts.Topic)
	if err != nil {
		return nil, err
	}

	queueName, ephemeral := backends.SubscriptionName(opts)

	queueURL, err := ensureQueue(ctx, a.sqsc, queueName)
	if err != nil {
		return nil, err
	}
	if ephemeral {
		a.ephemeralQueues = append(a.ephemeralQueues, queueURL)
	}

	qARN, err := queueARN(ctx, a.sqsc, queueURL)
	if err != nil {
		return nil, err
	}

	policy := allowSNSPolicy(qARN, topicARN)
	_, err = a.sqsc.SetQueueAttributes(ctx, &sqs.SetQueueAttributesInput{
		QueueUrl: &queueURL,
		Attributes: map[string]string{
			string(sqstypes.QueueAttributeNamePolicy): policy,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("setting queue policy: %w", err)
	}

	rawDelivery := "true"
	subOut, err := a.snsc.Subscribe(ctx, &sns.SubscribeInput{
		TopicArn:              &topicARN,
		Protocol:              strPtr("sqs"),
		Endpoint:              &qARN,
		Attributes:            map[string]string{"RawMessageDelivery": rawDelivery},
		ReturnSubscriptionArn: true,
	})
	if err != nil {
		return nil, fmt.Errorf("subscribing queue to topic: %w", err)
	}
	if subOut.SubscriptionArn != nil {
		a.subscriptions = append(a.subscriptions, *subOut.SubscriptionArn)
	}

	timeout := backends.TimeoutDuration(opts.Timeout, opts.Wait)
	return pollSQS(ctx, a.sqsc, queueURL, timeout, true, "subscriber queue "+queueName)
}

func (a *TopicAdapter) Close() error {
	ctx := context.Background()

	for _, subARN := range a.subscriptions {
		a.snsc.Unsubscribe(ctx, &sns.UnsubscribeInput{
			SubscriptionArn: &subARN,
		}) //nolint:errcheck
	}

	for _, queueURL := range a.ephemeralQueues {
		a.sqsc.DeleteQueue(ctx, &sqs.DeleteQueueInput{
			QueueUrl: &queueURL,
		}) //nolint:errcheck
	}

	return nil
}

