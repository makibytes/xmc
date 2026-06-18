//go:build awsmc

package awssqs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

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

	_, err = a.snsc.Publish(ctx, &sns.PublishInput{
		TopicArn:          &topicARN,
		Message:           &body,
		MessageAttributes: attrs,
	})
	return err
}

func (a *TopicAdapter) Subscribe(ctx context.Context, opts backends.SubscribeOptions) (*backends.Message, error) {
	topicARN, err := ensureTopic(ctx, a.snsc, opts.Topic)
	if err != nil {
		return nil, err
	}

	var queueName string
	ephemeral := false
	if opts.GroupID != "" {
		queueName = opts.GroupID
	} else if opts.Durable {
		queueName = fmt.Sprintf("xmc-durable-%s", opts.Topic)
	} else {
		queueName = fmt.Sprintf("xmc-sub-%s", randomSuffix())
		ephemeral = true
	}

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
	deadline := time.Now().Add(timeout)

	allAttrs := "All"
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, backends.ErrNoMessageAvailable
		}

		waitSecs := int32(remaining.Seconds())
		if waitSecs > 20 {
			waitSecs = 20
		}
		if waitSecs < 1 {
			waitSecs = 1
		}

		out, err := a.sqsc.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
			QueueUrl:              &queueURL,
			MaxNumberOfMessages:   1,
			WaitTimeSeconds:       waitSecs,
			MessageAttributeNames: []string{allAttrs},
		})
		if err != nil {
			return nil, fmt.Errorf("receiving from subscriber queue: %w", err)
		}

		if len(out.Messages) == 0 {
			if time.Now().After(deadline) {
				return nil, backends.ErrNoMessageAvailable
			}
			continue
		}

		msg := out.Messages[0]
		if msg.ReceiptHandle != nil {
			a.sqsc.DeleteMessage(ctx, &sqs.DeleteMessageInput{
				QueueUrl:      &queueURL,
				ReceiptHandle: msg.ReceiptHandle,
			}) //nolint:errcheck
		}

		return sqsToBackendMessage(msg), nil
	}
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

func randomSuffix() string {
	var b [6]byte
	rand.Read(b[:]) //nolint:errcheck
	return hex.EncodeToString(b[:])
}
