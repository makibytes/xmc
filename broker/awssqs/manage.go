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

// ListTopicsWithSubscriptions returns SNS topics as ObjectNodes with their
// subscriptions as children (for the AI TUI hierarchical sidebar window).
func ListTopicsWithSubscriptions(args ConnArguments) ([]backends.ObjectNode, error) {
	_, snsc, err := Connect(context.Background(), args)
	if err != nil {
		return nil, err
	}

	ctx := context.Background()
	out, err := snsc.ListTopics(ctx, &sns.ListTopicsInput{})
	if err != nil {
		return nil, fmt.Errorf("listing topics: %w", err)
	}

	var nodes []backends.ObjectNode
	for _, t := range out.Topics {
		if t.TopicArn == nil {
			continue
		}
		parts := strings.Split(*t.TopicArn, ":")
		name := parts[len(parts)-1]
		node := backends.ObjectNode{Name: name}

		subOut, subErr := snsc.ListSubscriptionsByTopic(ctx, &sns.ListSubscriptionsByTopicInput{
			TopicArn: t.TopicArn,
		})
		if subErr == nil {
			for _, s := range subOut.Subscriptions {
				ep := derefStr(s.Endpoint)
				proto := derefStr(s.Protocol)
				// For SQS endpoints show only the queue name (last URL segment).
				if proto == "sqs" {
					segs := strings.Split(ep, "/")
					ep = segs[len(segs)-1]
				}
				node.Children = append(node.Children, backends.ObjectNode{
					Name: ep,
					Kind: proto,
				})
			}
		}
		nodes = append(nodes, node)
	}
	return nodes, nil
}

// ListTopics returns SNS topics.
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

	topics := make([]backends.TopicInfo, 0, len(out.Topics))
	for _, t := range out.Topics {
		if t.TopicArn == nil {
			continue
		}
		parts := strings.Split(*t.TopicArn, ":")
		topics = append(topics, backends.TopicInfo{Name: parts[len(parts)-1]})
	}
	return topics, nil
}

// lookupTopicARN resolves a topic name to its SNS ARN by listing all topics.
func lookupTopicARN(ctx context.Context, snsc *sns.Client, name string) (string, error) {
	out, err := snsc.ListTopics(ctx, &sns.ListTopicsInput{})
	if err != nil {
		return "", fmt.Errorf("listing topics: %w", err)
	}
	for _, t := range out.Topics {
		if t.TopicArn == nil {
			continue
		}
		parts := strings.Split(*t.TopicArn, ":")
		if parts[len(parts)-1] == name {
			return *t.TopicArn, nil
		}
	}
	return "", fmt.Errorf("topic %s not found", name)
}

// CreateQueue creates an SQS queue (FIFO when name ends in ".fifo").
func CreateQueue(args ConnArguments, name string) error {
	sqsc, _, err := Connect(context.Background(), args)
	if err != nil {
		return err
	}
	_, err = ensureQueue(context.Background(), sqsc, name)
	return err
}

// DeleteQueue deletes an SQS queue by name.
func DeleteQueue(args ConnArguments, name string) error {
	sqsc, _, err := Connect(context.Background(), args)
	if err != nil {
		return err
	}
	ctx := context.Background()
	urlOut, err := sqsc.GetQueueUrl(ctx, &sqs.GetQueueUrlInput{QueueName: &name})
	if err != nil {
		return fmt.Errorf("getting queue URL for %s: %w", name, err)
	}
	_, err = sqsc.DeleteQueue(ctx, &sqs.DeleteQueueInput{QueueUrl: urlOut.QueueUrl})
	if err != nil {
		return fmt.Errorf("deleting queue %s: %w", name, err)
	}
	return nil
}

// CreateTopic creates an SNS topic.
func CreateTopic(args ConnArguments, name string) error {
	_, snsc, err := Connect(context.Background(), args)
	if err != nil {
		return err
	}
	_, err = ensureTopic(context.Background(), snsc, name)
	return err
}

// DeleteTopic deletes an SNS topic by name (resolved to ARN via list).
func DeleteTopic(args ConnArguments, name string) error {
	_, snsc, err := Connect(context.Background(), args)
	if err != nil {
		return err
	}
	ctx := context.Background()
	arn, err := lookupTopicARN(ctx, snsc, name)
	if err != nil {
		return err
	}
	_, err = snsc.DeleteTopic(ctx, &sns.DeleteTopicInput{TopicArn: &arn})
	if err != nil {
		return fmt.Errorf("deleting topic %s: %w", name, err)
	}
	return nil
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
