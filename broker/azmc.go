//go:build azure

package broker

import (
	"context"
	"os"

	azpkg "github.com/makibytes/xmc/broker/azuresb"
	"github.com/makibytes/xmc/broker/backends"
	"github.com/makibytes/xmc/cmd"
	"github.com/makibytes/xmc/mcp"
	"github.com/spf13/cobra"
)

// GetRootCommand builds the Azure Service Bus root cobra command.
func GetRootCommand() *cobra.Command {
	var connArgs azpkg.ConnArguments

	return cmd.NewRootCommand(cmd.BrokerSpec{
		Use:              "azmc",
		Short:            "Azure Service Bus Messaging Client",
		Long:             "Command-line interface for Azure Service Bus messaging",
		AIContext:        AIDoc("azure"),
		UnsupportedFlags: []string{"priority", "persistent", "selector"},
		ConsumeFlags: func(c *cobra.Command) {
			c.Flags().String("subscription", "", "Named subscription for topic consume (overrides -g)")
		},
		ConsumeExtra: func(c *cobra.Command) map[string]string {
			extra := make(map[string]string)
			if s, _ := c.Flags().GetString("subscription"); s != "" {
				extra["subscription"] = s
			}
			return extra
		},
		RegisterFlags: func(c *cobra.Command) {
			c.PersistentFlags().StringVarP(&connArgs.ConnectionString, "connection-string", "s", os.Getenv("AZMC_CONNECTION_STRING"), "Service Bus connection string")
			c.PersistentFlags().StringVar(&connArgs.Namespace, "namespace", os.Getenv("AZMC_NAMESPACE"), "Service Bus namespace FQDN (uses Azure AD)")
		},
		Queue: func() (backends.QueueBackend, error) { return azpkg.NewQueueAdapter(connArgs) },
		Topic: func() (backends.TopicBackend, error) { return azpkg.NewTopicAdapter(connArgs) },
		Ping:  func() (cmd.Closeable, error) { return azpkg.NewQueueAdapter(connArgs) },
		ManageSpec: &cmd.ManageSpec{
			Objects: []cmd.ObjectType{
				{
					Label: "Queues",
					Drain: true,
					List: func() ([]backends.ObjectNode, error) {
						queues, err := azpkg.ListQueues(connArgs)
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
					Publish:      true,
					ChildKind:    "subscription",
					List: func() ([]backends.ObjectNode, error) {
						return azpkg.ListTopicsWithSubscriptions(connArgs)
					},
				},
			},
			Purge: func(queue string) (int64, error) { return azpkg.PurgeQueue(connArgs, queue) },
			PurgeSubscription: func(topic, sub string) (int64, error) {
				return azpkg.PurgeSubscription(connArgs, topic, sub)
			},
			Stats:       func(queue string) (*backends.QueueStats, error) { return azpkg.GetQueueStats(connArgs, queue) },
			CreateQueue: &cmd.ManageAction{Run: func(q string) error { return azpkg.CreateQueue(connArgs, q) }},
			DeleteQueue: &cmd.ManageAction{Run: func(q string) error { return azpkg.DeleteQueue(connArgs, q) }},
			CreateTopic: &cmd.ManageAction{Run: func(t string) error { return azpkg.CreateTopic(connArgs, t) }},
			DeleteTopic: &cmd.ManageAction{Run: func(t string) error { return azpkg.DeleteTopic(connArgs, t) }},
		},
		Extra: []*cobra.Command{
			mcp.NewCommand(mcp.Deps{
				ServerName:    "xmc-azure",
				ServerVersion: cmd.Version(),
				Target:        connArgs.Namespace,
				NewQueue: func() (backends.QueueBackend, error) {
					return azpkg.NewQueueAdapter(connArgs)
				},
				NewTopic: func() (backends.TopicBackend, error) {
					return azpkg.NewTopicAdapter(connArgs)
				},
				ListQueues: func(_ context.Context) ([]backends.QueueInfo, error) {
					return azpkg.ListQueues(connArgs)
				},
				PurgeQueue: func(_ context.Context, queue string) (int64, error) {
					return azpkg.PurgeQueue(connArgs, queue)
				},
				ListTopics: func(_ context.Context) ([]backends.TopicInfo, error) {
					return azpkg.ListTopics(connArgs)
				},
				QueueStats: func(_ context.Context, queue string) (*backends.QueueStats, error) {
					return azpkg.GetQueueStats(connArgs, queue)
				},
			}),
		},
	})
}
