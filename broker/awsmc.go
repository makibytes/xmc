//go:build aws

package broker

import (
	"context"
	"fmt"
	"os"

	"github.com/makibytes/xmc/broker/backends"
	awspkg "github.com/makibytes/xmc/broker/awssqs"
	"github.com/makibytes/xmc/cmd"
	"github.com/makibytes/xmc/mcp"
	"github.com/spf13/cobra"
)

func GetRootCommand() *cobra.Command {
	var connArgs awspkg.ConnArguments

	defaultRegion := os.Getenv("AWSMC_REGION")
	if defaultRegion == "" {
		defaultRegion = "us-east-1"
	}

	return cmd.NewRootCommand(cmd.BrokerSpec{
		Use:       "awsmc",
		Short:     "AWS SQS/SNS Messaging Client",
		Long:      "Command-line interface for AWS SQS (queues) and SNS (topics)",
		AIContext: AIDoc("aws"),
		ProduceFlags: func(c *cobra.Command) {
			c.Flags().Bool("fifo", false, "Send to a FIFO queue")
			c.Flags().String("message-group-id", "", "Message group ID for FIFO queues")
			c.Flags().String("dedup-id", "", "Deduplication ID for FIFO queues")
		},
		ProduceExtra: func(c *cobra.Command) map[string]string {
			extra := make(map[string]string)
			if fifo, _ := c.Flags().GetBool("fifo"); fifo {
				extra["fifo"] = "true"
			}
			if gid, _ := c.Flags().GetString("message-group-id"); gid != "" {
				extra["message-group-id"] = gid
			}
			if did, _ := c.Flags().GetString("dedup-id"); did != "" {
				extra["dedup-id"] = did
			}
			return extra
		},
		ConsumeFlags: func(c *cobra.Command) {
			c.Flags().Int("visibility-timeout", 30, "Visibility timeout in seconds")
		},
		ConsumeExtra: func(c *cobra.Command) map[string]string {
			extra := make(map[string]string)
			if vt, _ := c.Flags().GetInt("visibility-timeout"); vt != 30 {
				extra["visibility-timeout"] = fmt.Sprintf("%d", vt)
			}
			return extra
		},
		RegisterFlags: func(c *cobra.Command) {
			c.PersistentFlags().StringVarP(&connArgs.Region, "region", "s", defaultRegion, "AWS region")
			c.PersistentFlags().StringVar(&connArgs.Endpoint, "endpoint", os.Getenv("AWSMC_ENDPOINT"), "Custom endpoint URL (e.g. for LocalStack)")
			c.PersistentFlags().StringVar(&connArgs.Profile, "profile", os.Getenv("AWSMC_PROFILE"), "AWS shared config profile name")
		},
		Queue: func() (backends.QueueBackend, error) { return awspkg.NewQueueAdapter(connArgs) },
		Topic: func() (backends.TopicBackend, error) { return awspkg.NewTopicAdapter(connArgs) },
		Ping:  func() (cmd.Closeable, error) { return awspkg.NewQueueAdapter(connArgs) },
		ManageSpec: &cmd.ManageSpec{
			Objects: []cmd.ObjectType{
				{
					Label: "Queues",
					List: func() ([]backends.ObjectNode, error) {
						queues, err := awspkg.ListQueues(connArgs)
						if err != nil {
							return nil, err
						}
						out := make([]backends.ObjectNode, len(queues))
						for i, q := range queues {
							out[i] = backends.ObjectNode{
								Name:    q.Name,
								Metrics: []backends.Metric{{Label: "msgs", Value: q.MessageCount}},
							}
						}
						return out, nil
					},
				},
				{
					Label:        "Topics",
					Hierarchical: true,
					List: func() ([]backends.ObjectNode, error) {
						return awspkg.ListTopicsWithSubscriptions(connArgs)
					},
				},
			},
			Purge:       func(queue string) (int64, error) { return awspkg.PurgeQueue(connArgs, queue) },
			Stats:       func(queue string) (*backends.QueueStats, error) { return awspkg.GetQueueStats(connArgs, queue) },
			CreateQueue: &cmd.ManageAction{Run: func(q string) error { return awspkg.CreateQueue(connArgs, q) }},
			DeleteQueue: &cmd.ManageAction{Run: func(q string) error { return awspkg.DeleteQueue(connArgs, q) }},
			CreateTopic: &cmd.ManageAction{Run: func(t string) error { return awspkg.CreateTopic(connArgs, t) }},
			DeleteTopic: &cmd.ManageAction{Run: func(t string) error { return awspkg.DeleteTopic(connArgs, t) }},
		},
		Extra: []*cobra.Command{
			mcp.NewCommand(mcp.Deps{
				ServerName:    "xmc-aws",
				ServerVersion: cmd.Version(),
				Target:        "aws-sqs://" + connArgs.Region,
				NewQueue: func() (backends.QueueBackend, error) {
					return awspkg.NewQueueAdapter(connArgs)
				},
				NewTopic: func() (backends.TopicBackend, error) {
					return awspkg.NewTopicAdapter(connArgs)
				},
				ListQueues: func(_ context.Context) ([]mcp.QueueInfo, error) {
					queues, err := awspkg.ListQueues(connArgs)
					if err != nil {
						return nil, err
					}
					out := make([]mcp.QueueInfo, len(queues))
					for i, q := range queues {
						out[i] = mcp.QueueInfo{Name: q.Name, MessageCount: q.MessageCount}
					}
					return out, nil
				},
				PurgeQueue: func(_ context.Context, queue string) (int64, error) {
					return awspkg.PurgeQueue(connArgs, queue)
				},
				QueueStats: func(_ context.Context, queue string) (*mcp.QueueStats, error) {
					s, err := awspkg.GetQueueStats(connArgs, queue)
					if err != nil {
						return nil, err
					}
					return &mcp.QueueStats{
						Name: s.Name, MessageCount: s.MessageCount, ConsumerCount: int64(s.ConsumerCount),
						EnqueueCount: s.EnqueueCount, DequeueCount: s.DequeueCount,
					}, nil
				},
			}),
		},
	})
}
