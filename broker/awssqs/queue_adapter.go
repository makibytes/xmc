//go:build awsmc

package awssqs

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/sqs"

	"github.com/makibytes/xmc/broker/backends"
)

type QueueAdapter struct {
	sqsc *sqs.Client
	urls map[string]string
}

func NewQueueAdapter(args ConnArguments) (*QueueAdapter, error) {
	sqsc, _, err := Connect(context.Background(), args)
	if err != nil {
		return nil, err
	}
	return &QueueAdapter{
		sqsc: sqsc,
		urls: make(map[string]string),
	}, nil
}

func (a *QueueAdapter) Send(ctx context.Context, opts backends.SendOptions) error {
	url, err := a.getQueueURL(ctx, opts.Queue)
	if err != nil {
		return err
	}

	body := string(opts.Message)
	attrs := sqsAttributes(opts.Properties,
		opts.MessageID, opts.CorrelationID, opts.ReplyTo, opts.ContentType)

	_, err = a.sqsc.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:          &url,
		MessageBody:       &body,
		MessageAttributes: attrs,
	})
	return err
}

func (a *QueueAdapter) Receive(ctx context.Context, opts backends.ReceiveOptions) (*backends.Message, error) {
	url, err := a.getQueueURL(ctx, opts.Queue)
	if err != nil {
		return nil, err
	}

	timeout := backends.TimeoutDuration(opts.Timeout, opts.Wait)
	deadline := time.Now().Add(timeout)

	var visibilityTimeout int32 = 30
	if !opts.Acknowledge {
		visibilityTimeout = 0
	}

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
			QueueUrl:              &url,
			MaxNumberOfMessages:   1,
			WaitTimeSeconds:       waitSecs,
			VisibilityTimeout:     visibilityTimeout,
			MessageAttributeNames: []string{allAttrs},
		})
		if err != nil {
			return nil, fmt.Errorf("receiving from queue %s: %w", opts.Queue, err)
		}

		if len(out.Messages) == 0 {
			if time.Now().After(deadline) {
				return nil, backends.ErrNoMessageAvailable
			}
			continue
		}

		msg := out.Messages[0]

		if opts.Acknowledge && msg.ReceiptHandle != nil {
			_, err := a.sqsc.DeleteMessage(ctx, &sqs.DeleteMessageInput{
				QueueUrl:      &url,
				ReceiptHandle: msg.ReceiptHandle,
			})
			if err != nil {
				return nil, fmt.Errorf("acknowledging message: %w", err)
			}
		} else if !opts.Acknowledge && msg.ReceiptHandle != nil {
			// Make the message immediately visible again for other consumers.
			a.sqsc.ChangeMessageVisibility(ctx, &sqs.ChangeMessageVisibilityInput{
				QueueUrl:          &url,
				ReceiptHandle:     msg.ReceiptHandle,
				VisibilityTimeout: 0,
			}) //nolint:errcheck
		}

		return sqsToBackendMessage(msg), nil
	}
}

func (a *QueueAdapter) Close() error {
	return nil
}

func (a *QueueAdapter) getQueueURL(ctx context.Context, name string) (string, error) {
	if url, ok := a.urls[name]; ok {
		return url, nil
	}
	url, err := ensureQueue(ctx, a.sqsc, name)
	if err != nil {
		return "", err
	}
	a.urls[name] = url
	return url, nil
}
