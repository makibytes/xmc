//go:build azure

package broker

import (
	"os"

	"github.com/makibytes/xmc/broker/backends"
	azpkg "github.com/makibytes/xmc/broker/azuresb"
	"github.com/makibytes/xmc/cmd"
	"github.com/spf13/cobra"
)

func GetRootCommand() *cobra.Command {
	var connArgs azpkg.ConnArguments

	return cmd.NewRootCommand(cmd.BrokerSpec{
		Use:       "azmc",
		Short:     "Azure Service Bus Messaging Client",
		Long:      "Command-line interface for Azure Service Bus messaging",
		AIContext: AIDoc("azure"),
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
					List: func() ([]backends.ObjectNode, error) {
						return azpkg.ListTopicsWithSubscriptions(connArgs)
					},
				},
			},
			Purge: func(queue string) (int64, error) { return azpkg.PurgeQueue(connArgs, queue) },
			Stats: func(queue string) (*backends.QueueStats, error) { return azpkg.GetQueueStats(connArgs, queue) },
		},
	})
}
