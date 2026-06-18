//go:build awsmc

package broker

import (
	"fmt"
	"os"

	"github.com/makibytes/xmc/broker/backends"
	awspkg "github.com/makibytes/xmc/broker/awssqs"
	"github.com/makibytes/xmc/cmd"
	"github.com/makibytes/xmc/log"
	"github.com/spf13/cobra"
)

func GetRootCommand() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "awsmc",
		Short: "AWS SQS/SNS Messaging Client",
		Long:  "Command-line interface for AWS SQS (queues) and SNS (topics)",
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			if verbose, _ := cmd.Flags().GetBool("verbose"); verbose {
				log.IsVerbose = true
			}
		},
	}

	defaultRegion := os.Getenv("AWSMC_REGION")
	if defaultRegion == "" {
		defaultRegion = "us-east-1"
	}
	defaultEndpoint := os.Getenv("AWSMC_ENDPOINT")
	defaultProfile := os.Getenv("AWSMC_PROFILE")

	var connArgs awspkg.ConnArguments

	rootCmd.PersistentFlags().StringVarP(&connArgs.Region, "region", "s", defaultRegion, "AWS region")
	rootCmd.PersistentFlags().StringVar(&connArgs.Endpoint, "endpoint", defaultEndpoint, "Custom endpoint URL (e.g. for LocalStack)")
	rootCmd.PersistentFlags().StringVar(&connArgs.Profile, "profile", defaultProfile, "AWS shared config profile name")
	rootCmd.PersistentFlags().BoolP("verbose", "v", false, "Print verbose output")

	queueFactory := cmd.QueueAdapterFactory(func() (backends.QueueBackend, error) {
		return awspkg.NewQueueAdapter(connArgs)
	})
	rootCmd.AddCommand(cmd.WrapQueueCommand(cmd.NewSendCommand, queueFactory))
	rootCmd.AddCommand(cmd.WrapQueueCommand(cmd.NewReceiveCommand, queueFactory))
	rootCmd.AddCommand(cmd.WrapQueueCommand(cmd.NewPeekCommand, queueFactory))
	rootCmd.AddCommand(cmd.WrapQueueCommand(cmd.NewRequestCommand, queueFactory))
	rootCmd.AddCommand(cmd.WrapQueueCommand(cmd.NewReplyCommand, queueFactory))
	rootCmd.AddCommand(cmd.WrapQueueCommand(cmd.NewMoveCommand, queueFactory))
	rootCmd.AddCommand(cmd.WrapQueueCommand(cmd.NewForwardCommand, queueFactory))

	topicFactory := cmd.TopicAdapterFactory(func() (backends.TopicBackend, error) {
		return awspkg.NewTopicAdapter(connArgs)
	})
	rootCmd.AddCommand(cmd.WrapTopicCommand(cmd.NewPublishCommand, topicFactory))
	rootCmd.AddCommand(cmd.WrapTopicCommand(cmd.NewSubscribeCommand, topicFactory))

	rootCmd.AddCommand(newAWSManageCommand(&connArgs))

	rootCmd.AddCommand(cmd.NewPingCommand(func() (cmd.Closeable, error) {
		return awspkg.NewQueueAdapter(connArgs)
	}))

	rootCmd.AddCommand(cmd.NewVersionCommand())

	return rootCmd
}

func newAWSManageCommand(connArgs *awspkg.ConnArguments) *cobra.Command {
	mgmtCmd := &cobra.Command{
		Use:   "manage",
		Short: "Broker management operations",
	}

	mgmtCmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List queues and topics",
		RunE: func(c *cobra.Command, args []string) error {
			queues, err := awspkg.ListQueues(*connArgs)
			if err != nil {
				return err
			}
			for _, q := range queues {
				fmt.Printf("queue  %-40s  messages=%d\n", q.Name, q.MessageCount)
			}
			topics, err := awspkg.ListTopics(*connArgs)
			if err != nil {
				return err
			}
			for _, t := range topics {
				fmt.Printf("topic  %s\n", t.Name)
			}
			return nil
		},
	})

	mgmtCmd.AddCommand(&cobra.Command{
		Use:   "purge [queue]",
		Short: "Purge all messages from a queue",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			count, err := awspkg.PurgeQueue(*connArgs, args[0])
			if err != nil {
				return err
			}
			fmt.Printf("Purged ~%d messages from queue %s\n", count, args[0])
			return nil
		},
	})

	mgmtCmd.AddCommand(&cobra.Command{
		Use:   "stats [queue]",
		Short: "Show queue statistics",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			stats, err := awspkg.GetQueueStats(*connArgs, args[0])
			if err != nil {
				return err
			}
			fmt.Printf("Queue:              %s\n", stats.Name)
			fmt.Printf("Messages (visible): %d\n", stats.MessageCount)
			fmt.Printf("Messages (total):   %d\n", stats.EnqueueCount)
			return nil
		},
	})

	return mgmtCmd
}
