//go:build aws

package broker

import (
	"os"

	"github.com/makibytes/xmc/broker/backends"
	awspkg "github.com/makibytes/xmc/broker/awssqs"
	"github.com/makibytes/xmc/cmd"
	"github.com/spf13/cobra"
)

func GetRootCommand() *cobra.Command {
	var connArgs awspkg.ConnArguments

	defaultRegion := os.Getenv("AWSMC_REGION")
	if defaultRegion == "" {
		defaultRegion = "us-east-1"
	}

	return cmd.NewRootCommand(cmd.BrokerSpec{
		Use:   "awsmc",
		Short: "AWS SQS/SNS Messaging Client",
		Long:  "Command-line interface for AWS SQS (queues) and SNS (topics)",
		RegisterFlags: func(c *cobra.Command) {
			c.PersistentFlags().StringVarP(&connArgs.Region, "region", "s", defaultRegion, "AWS region")
			c.PersistentFlags().StringVar(&connArgs.Endpoint, "endpoint", os.Getenv("AWSMC_ENDPOINT"), "Custom endpoint URL (e.g. for LocalStack)")
			c.PersistentFlags().StringVar(&connArgs.Profile, "profile", os.Getenv("AWSMC_PROFILE"), "AWS shared config profile name")
		},
		Queue: func() (backends.QueueBackend, error) { return awspkg.NewQueueAdapter(connArgs) },
		Topic: func() (backends.TopicBackend, error) { return awspkg.NewTopicAdapter(connArgs) },
		Ping:  func() (cmd.Closeable, error) { return awspkg.NewQueueAdapter(connArgs) },
		Manage: cmd.NewManageCommand(cmd.ManageSpec{
			ListQueues: func() ([]backends.QueueInfo, error) { return awspkg.ListQueues(connArgs) },
			ListTopics: func() ([]backends.TopicInfo, error) { return awspkg.ListTopics(connArgs) },
			Purge:      func(queue string) (int64, error) { return awspkg.PurgeQueue(connArgs, queue) },
			Stats:      func(queue string) (*backends.QueueStats, error) { return awspkg.GetQueueStats(connArgs, queue) },
		}),
	})
}
