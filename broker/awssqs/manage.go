//go:build aws

package awssqs

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"github.com/makibytes/xmc/broker/backends"
)

func ListQueues(args ConnArguments) ([]backends.QueueInfo, error) {
	sqsc, _, err := Connect(context.Background(), args)
	if err != nil {
		return nil, err
	}

	ctx := context.Background()
	out, err := sqsc.ListQueues(ctx, &sqs.ListQueuesInput{})
	if err != nil {
		return nil, fmt.Errorf("listing queues: %w", err)
	}

	var queues []backends.QueueInfo
	for _, url := range out.QueueUrls {
		parts := strings.Split(url, "/")
		name := parts[len(parts)-1]

		count := int64(0)
		attrs, err := sqsc.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
			QueueUrl: &url,
			AttributeNames: []sqstypes.QueueAttributeName{
				sqstypes.QueueAttributeNameApproximateNumberOfMessages,
			},
		})
		if err == nil {
			if v, ok := attrs.Attributes[string(sqstypes.QueueAttributeNameApproximateNumberOfMessages)]; ok {
				count, _ = strconv.ParseInt(v, 10, 64)
			}
		}

		queues = append(queues, backends.QueueInfo{
			Name:         name,
			MessageCount: count,
		})
	}

	return queues, nil
}

func ListTopics(args ConnArguments) ([]backends.TopicInfo, error) {
	_, snsc, err := Connect(context.Background(), args)
	if err != nil {
		return nil, err
	}

	ctx := context.Background()
	out, err := snsc.ListTopics(ctx, &sns.ListTopicsInput{})
	if err != nil {
		return nil, fmt.Errorf("listing topics: %w", err)
	}

	var topics []backends.TopicInfo
	for _, t := range out.Topics {
		if t.TopicArn == nil {
			continue
		}
		parts := strings.Split(*t.TopicArn, ":")
		name := parts[len(parts)-1]
		topics = append(topics, backends.TopicInfo{Name: name})
	}

	return topics, nil
}

func PurgeQueue(args ConnArguments, queue string) (int64, error) {
	sqsc, _, err := Connect(context.Background(), args)
	if err != nil {
		return 0, err
	}

	ctx := context.Background()
	url, err := ensureQueue(ctx, sqsc, queue)
	if err != nil {
		return 0, err
	}

	attrs, err := sqsc.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
		QueueUrl: &url,
		AttributeNames: []sqstypes.QueueAttributeName{
			sqstypes.QueueAttributeNameApproximateNumberOfMessages,
		},
	})
	var count int64
	if err == nil {
		if v, ok := attrs.Attributes[string(sqstypes.QueueAttributeNameApproximateNumberOfMessages)]; ok {
			count, _ = strconv.ParseInt(v, 10, 64)
		}
	}

	_, err = sqsc.PurgeQueue(ctx, &sqs.PurgeQueueInput{
		QueueUrl: &url,
	})
	if err != nil {
		return 0, fmt.Errorf("purging queue %s: %w", queue, err)
	}

	return count, nil
}

func GetQueueStats(args ConnArguments, queue string) (*backends.QueueStats, error) {
	sqsc, _, err := Connect(context.Background(), args)
	if err != nil {
		return nil, err
	}

	ctx := context.Background()
	url, err := ensureQueue(ctx, sqsc, queue)
	if err != nil {
		return nil, err
	}

	attrs, err := sqsc.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
		QueueUrl: &url,
		AttributeNames: []sqstypes.QueueAttributeName{
			sqstypes.QueueAttributeNameApproximateNumberOfMessages,
			sqstypes.QueueAttributeNameApproximateNumberOfMessagesNotVisible,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("getting queue stats: %w", err)
	}

	parseInt := func(key string) int64 {
		v, _ := strconv.ParseInt(attrs.Attributes[key], 10, 64)
		return v
	}

	visible := parseInt(string(sqstypes.QueueAttributeNameApproximateNumberOfMessages))
	invisible := parseInt(string(sqstypes.QueueAttributeNameApproximateNumberOfMessagesNotVisible))

	return &backends.QueueStats{
		Name:         queue,
		MessageCount: visible,
		EnqueueCount: visible + invisible,
	}, nil
}
