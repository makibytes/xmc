//go:build aws

package awssqs

import (
	"context"
	"net/http"
	"strconv"
	"strings"

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
	// --fifo flag: ensure the queue name carries the required .fifo suffix.
	if opts.Extra["fifo"] == "true" && !strings.HasSuffix(opts.Queue, ".fifo") {
		opts.Queue += ".fifo"
	}

	url, err := a.getQueueURL(ctx, opts.Queue)
	if err != nil {
		return err
	}

	body := string(opts.Message)
	attrs := sqsAttributes(opts.Properties,
		opts.MessageID, opts.CorrelationID, opts.ReplyTo, opts.ContentType)

	input := &sqs.SendMessageInput{
		QueueUrl:          &url,
		MessageBody:       &body,
		MessageAttributes: attrs,
	}

	if gid := opts.Extra["message-group-id"]; gid != "" {
		input.MessageGroupId = &gid
	} else if strings.HasSuffix(opts.Queue, ".fifo") {
		if opts.Key != "" {
			// -K maps to the FIFO ordering concept, MessageGroupId.
			input.MessageGroupId = &opts.Key
		} else {
			// FIFO queues require MessageGroupId; default to "xmc" when not set.
			defaultGID := "xmc"
			input.MessageGroupId = &defaultGID
		}
	}
	if did := opts.Extra["dedup-id"]; did != "" {
		input.MessageDeduplicationId = &did
	}

	_, err = a.sqsc.SendMessage(ctx, input)
	return err
}

func (a *QueueAdapter) Receive(ctx context.Context, opts backends.ReceiveOptions) (*backends.Message, error) {
	url, err := a.getQueueURL(ctx, opts.Queue)
	if err != nil {
		return nil, err
	}

	timeout := backends.TimeoutDuration(opts.Timeout, opts.Wait)

	var visTimeout int32
	if vt := opts.Extra["visibility-timeout"]; vt != "" {
		if n, err := strconv.Atoi(vt); err == nil {
			visTimeout = int32(n)
		}
	}

	return pollSQS(ctx, a.sqsc, url, timeout, opts.Acknowledge, "queue "+opts.Queue, visTimeout)
}

func (a *QueueAdapter) Close() error {
	if a.sqsc != nil {
		if hc, ok := a.sqsc.Options().HTTPClient.(*http.Client); ok && hc != nil {
			hc.CloseIdleConnections()
		}
	}
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
