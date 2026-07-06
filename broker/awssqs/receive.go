//go:build aws

package awssqs

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"github.com/makibytes/xmc/broker/backends"
)

// pollSQS performs a long-poll receive loop on the given SQS queue, waiting up
// to the specified timeout for a single message. SQS caps WaitTimeSeconds at
// 20, so we loop for longer timeouts.
//
// When acknowledge is true, the message is deleted (acked) before returning.
// When false, the message's visibility is immediately restored so it remains
// available to other consumers (peek semantics).
func pollSQS(ctx context.Context, sqsc *sqs.Client, queueURL string, timeout time.Duration, acknowledge bool, errLabel string, visOverride ...int32) (*backends.Message, error) {
	deadline := time.Now().Add(timeout)

	var visibilityTimeout int32 = 30
	if !acknowledge {
		visibilityTimeout = 0
	} else if len(visOverride) > 0 && visOverride[0] > 0 {
		visibilityTimeout = visOverride[0]
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

		out, err := sqsc.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
			QueueUrl:              &queueURL,
			MaxNumberOfMessages:   1,
			WaitTimeSeconds:       waitSecs,
			VisibilityTimeout:     visibilityTimeout,
			MessageAttributeNames: []string{allAttrs},
			// System attributes carry the SQS-assigned metadata we map back
			// (MessageGroupId → Key).
			MessageSystemAttributeNames: []sqstypes.MessageSystemAttributeName{
				sqstypes.MessageSystemAttributeNameAll,
			},
		})
		if err != nil {
			return nil, fmt.Errorf("receiving from %s: %w", errLabel, err)
		}

		if len(out.Messages) == 0 {
			if time.Now().After(deadline) {
				return nil, backends.ErrNoMessageAvailable
			}
			continue
		}

		msg := out.Messages[0]

		if acknowledge && msg.ReceiptHandle != nil {
			_, err := sqsc.DeleteMessage(ctx, &sqs.DeleteMessageInput{
				QueueUrl:      &queueURL,
				ReceiptHandle: msg.ReceiptHandle,
			})
			if err != nil {
				return nil, fmt.Errorf("acknowledging message: %w", err)
			}
		} else if !acknowledge && msg.ReceiptHandle != nil {
			// Make the message immediately visible again for other consumers.
			sqsc.ChangeMessageVisibility(ctx, &sqs.ChangeMessageVisibilityInput{
				QueueUrl:          &queueURL,
				ReceiptHandle:     msg.ReceiptHandle,
				VisibilityTimeout: 0,
			}) //nolint:errcheck
		}

		return sqsToBackendMessage(msg), nil
	}
}
