//go:build aws

package awssqs

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

type ConnArguments struct {
	Region   string
	Endpoint string
	Profile  string
}

func Connect(ctx context.Context, args ConnArguments) (*sqs.Client, *sns.Client, error) {
	var opts []func(*config.LoadOptions) error

	if args.Region != "" {
		opts = append(opts, config.WithRegion(args.Region))
	}
	if args.Profile != "" {
		opts = append(opts, config.WithSharedConfigProfile(args.Profile))
	}

	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("loading AWS config: %w", err)
	}

	sqsOpts := func(o *sqs.Options) {}
	snsOpts := func(o *sns.Options) {}
	if args.Endpoint != "" {
		sqsOpts = func(o *sqs.Options) { o.BaseEndpoint = &args.Endpoint }
		snsOpts = func(o *sns.Options) { o.BaseEndpoint = &args.Endpoint }
	}

	sqsc := sqs.NewFromConfig(cfg, sqsOpts)
	snsc := sns.NewFromConfig(cfg, snsOpts)

	return sqsc, snsc, nil
}

func ensureQueue(ctx context.Context, sqsc *sqs.Client, name string) (string, error) {
	input := &sqs.CreateQueueInput{QueueName: &name}
	if strings.HasSuffix(name, ".fifo") {
		// FIFO queues require these attributes to be set on creation.
		input.Attributes = map[string]string{
			string(sqstypes.QueueAttributeNameFifoQueue):                 "true",
			string(sqstypes.QueueAttributeNameContentBasedDeduplication): "true",
		}
	}
	out, err := sqsc.CreateQueue(ctx, input)
	if err != nil {
		return "", fmt.Errorf("creating/getting queue %s: %w", name, err)
	}
	return *out.QueueUrl, nil
}

func queueARN(ctx context.Context, sqsc *sqs.Client, queueURL string) (string, error) {
	out, err := sqsc.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
		QueueUrl:       &queueURL,
		AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameQueueArn},
	})
	if err != nil {
		return "", fmt.Errorf("getting queue ARN: %w", err)
	}
	arn, ok := out.Attributes[string(sqstypes.QueueAttributeNameQueueArn)]
	if !ok {
		return "", fmt.Errorf("queue ARN not found in attributes")
	}
	return arn, nil
}

func ensureTopic(ctx context.Context, snsc *sns.Client, name string) (string, error) {
	out, err := snsc.CreateTopic(ctx, &sns.CreateTopicInput{
		Name: &name,
	})
	if err != nil {
		return "", fmt.Errorf("creating/getting topic %s: %w", name, err)
	}
	return *out.TopicArn, nil
}

// allowSNSPolicy returns a JSON SQS policy that lets the given SNS topic
// publish messages to the given SQS queue.
func allowSNSPolicy(queueARN, topicARN string) string {
	policy := map[string]any{
		"Version": "2012-10-17",
		"Statement": []map[string]any{
			{
				"Effect":    "Allow",
				"Principal": map[string]string{"Service": "sns.amazonaws.com"},
				"Action":    "sqs:SendMessage",
				"Resource":  queueARN,
				"Condition": map[string]any{
					"ArnEquals": map[string]string{"aws:SourceArn": topicARN},
				},
			},
		},
	}
	b, _ := json.Marshal(policy)
	return string(b)
}
