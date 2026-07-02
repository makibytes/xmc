//go:build google

package broker

import (
	"context"
	"os"

	"github.com/makibytes/xmc/broker/backends"
	gcppkg "github.com/makibytes/xmc/broker/gcppubsub"
	"github.com/makibytes/xmc/cmd"
	"github.com/makibytes/xmc/mcp"
	"github.com/spf13/cobra"
)

func GetRootCommand() *cobra.Command {
	var connArgs gcppkg.ConnArguments

	return cmd.NewRootCommand(cmd.BrokerSpec{
		Use:              "gmc",
		Short:            "Google Pub/Sub Messaging Client",
		Long:             "Command-line interface for Google Cloud Pub/Sub messaging",
		AIContext:        AIDoc("google"),
		UnsupportedFlags: []string{"ttl", "priority", "persistent", "selector"},
		ConsumeFlags: func(c *cobra.Command) {
			c.Flags().String("subscription", "", "Named subscription override for receive/subscribe")
		},
		ConsumeExtra: func(c *cobra.Command) map[string]string {
			extra := make(map[string]string)
			if s, _ := c.Flags().GetString("subscription"); s != "" {
				extra["subscription"] = s
			}
			return extra
		},
		RegisterFlags: func(c *cobra.Command) {
			c.PersistentFlags().StringVarP(&connArgs.Project, "project", "s", os.Getenv("GMC_PROJECT"), "Google Cloud project ID")
			c.PersistentFlags().StringVar(&connArgs.Credentials, "credentials", os.Getenv("GMC_CREDENTIALS"), "Path to service account credentials JSON file")
			c.PersistentFlags().StringVar(&connArgs.Endpoint, "endpoint", os.Getenv("GMC_SERVER"), "Pub/Sub API endpoint (e.g. for emulator)")
		},
		Queue: func() (backends.QueueBackend, error) { return gcppkg.NewQueueAdapter(connArgs) },
		Topic: func() (backends.TopicBackend, error) { return gcppkg.NewTopicAdapter(connArgs) },
		Ping:  func() (cmd.Closeable, error) { return gcppkg.NewQueueAdapter(connArgs) },
		ManageSpec: &cmd.ManageSpec{
			Objects: []cmd.ObjectType{
				{
					Label: "Queues",
					List: func() ([]backends.ObjectNode, error) {
						queues, err := gcppkg.ListQueues(connArgs)
						if err != nil {
							return nil, err
						}
						out := make([]backends.ObjectNode, len(queues))
						for i, q := range queues {
							out[i] = backends.ObjectNode{Name: q.Name}
						}
						return out, nil
					},
				},
				{
					Label:        "Topics",
					Hierarchical: true,
					List: func() ([]backends.ObjectNode, error) {
						return gcppkg.ListTopicsWithSubscriptions(connArgs)
					},
				},
			},
			Purge: func(queue string) (int64, error) { return gcppkg.PurgeQueue(connArgs, queue) },
			PurgeSubscription: func(topic, sub string) (int64, error) {
				return gcppkg.PurgeSubscription(connArgs, topic, sub)
			},
			CreateQueue: &cmd.ManageAction{Run: func(q string) error { return gcppkg.CreateQueue(connArgs, q) }},
			DeleteQueue: &cmd.ManageAction{Run: func(q string) error { return gcppkg.DeleteQueue(connArgs, q) }},
			CreateTopic: &cmd.ManageAction{Run: func(t string) error { return gcppkg.CreateTopic(connArgs, t) }},
			DeleteTopic: &cmd.ManageAction{Run: func(t string) error { return gcppkg.DeleteTopic(connArgs, t) }},
		},
		Extra: []*cobra.Command{
			mcp.NewCommand(mcp.Deps{
				ServerName:    "xmc-google",
				ServerVersion: cmd.Version(),
				Target:        connArgs.Project,
				NewQueue: func() (backends.QueueBackend, error) {
					return gcppkg.NewQueueAdapter(connArgs)
				},
				NewTopic: func() (backends.TopicBackend, error) {
					return gcppkg.NewTopicAdapter(connArgs)
				},
				ListQueues: func(_ context.Context) ([]mcp.QueueInfo, error) {
					queues, err := gcppkg.ListQueues(connArgs)
					if err != nil {
						return nil, err
					}
					out := make([]mcp.QueueInfo, len(queues))
					for i, q := range queues {
						out[i] = mcp.QueueInfo{Name: q.Name}
					}
					return out, nil
				},
				PurgeQueue: func(_ context.Context, queue string) (int64, error) {
					return gcppkg.PurgeQueue(connArgs, queue)
				},
			}),
		},
	})
}
