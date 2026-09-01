//go:build nats

package broker

import (
	"context"
	"os"

	"github.com/makibytes/xmc/broker/backends"
	natspkg "github.com/makibytes/xmc/broker/nats"
	"github.com/makibytes/xmc/cmd"
	"github.com/makibytes/xmc/mcp"
	"github.com/spf13/cobra"
)

// GetRootCommand builds the NATS root cobra command.
func GetRootCommand() *cobra.Command {
	var connArgs natspkg.ConnArguments
	var retention string
	var maxMsgs int64
	var subjects []string

	defaultServer := os.Getenv("NMC_SERVER")
	if defaultServer == "" {
		defaultServer = "nats://localhost:4222"
	}

	var stream string

	return cmd.NewRootCommand(cmd.BrokerSpec{
		Use:              "nmc",
		Short:            "NATS Messaging Client",
		Long:             "Command-line interface for NATS messaging",
		AIContext:        AIDoc("nats"),
		UnsupportedFlags: []string{"ttl", "priority", "persistent", "selector"},
		ConsumeFlags: func(c *cobra.Command) {
			c.Flags().String("stream", "", "JetStream stream name override (default: auto-derived from queue name)")
		},
		ConsumeExtra: func(c *cobra.Command) map[string]string {
			extra := make(map[string]string)
			if s, _ := c.Flags().GetString("stream"); s != "" {
				extra["stream"] = s
			}
			if stream != "" && extra["stream"] == "" {
				extra["stream"] = stream
			}
			return extra
		},
		ProduceFlags: func(c *cobra.Command) {
			c.Flags().String("stream", "", "JetStream stream name override (default: auto-derived from queue name)")
		},
		ProduceExtra: func(c *cobra.Command) map[string]string {
			extra := make(map[string]string)
			if s, _ := c.Flags().GetString("stream"); s != "" {
				extra["stream"] = s
			}
			if stream != "" && extra["stream"] == "" {
				extra["stream"] = stream
			}
			return extra
		},
		RegisterFlags: func(c *cobra.Command) {
			backends.RegisterCommonFlags(c, &connArgs.Server, &connArgs.User, &connArgs.Password, "NMC_", defaultServer,
				"Server URL", "Username for authentication", "Password for authentication")
			c.PersistentFlags().StringVar(&stream, "stream", "", "Default JetStream stream name (applied when --stream on verb is not set)")
			backends.RegisterTLSFlags(c, &connArgs.TLS)
		},
		Queue: func() (backends.QueueBackend, error) { return natspkg.NewQueueAdapter(connArgs) },
		Topic: func() (backends.TopicBackend, error) { return natspkg.NewTopicAdapter(connArgs) },
		Ping:  func() (cmd.Closeable, error) { return natspkg.NewQueueAdapter(connArgs) },
		ManageSpec: &cmd.ManageSpec{
			Objects: []cmd.ObjectType{
				{
					Label:        "Streams",
					Hierarchical: true,
					Drain:        true,
					List: func() ([]backends.ObjectNode, error) {
						return natspkg.ListStreamsWithConsumers(connArgs)
					},
				},
			},
			CreateQueue: &cmd.ManageAction{
				SetupFlags: func(c *cobra.Command) {
					c.Flags().StringVar(&retention, "retention", "workqueue", "Stream retention policy (workqueue, limits, interest)")
					c.Flags().Int64Var(&maxMsgs, "max-msgs", 0, "Maximum number of messages (0 = unlimited)")
					c.Flags().StringSliceVar(&subjects, "subject", nil, "NATS subjects to bind (default: xmc.queue.<name>)")
				},
				Run: func(queue string) error { return natspkg.CreateStream(connArgs, queue, retention, maxMsgs, subjects) },
			},
			DeleteQueue: &cmd.ManageAction{Run: func(queue string) error { return natspkg.DeleteStream(connArgs, queue) }},
			Purge:       func(queue string) (int64, error) { return natspkg.PurgeStream(connArgs, queue) },
			Stats:       func(queue string) (*backends.QueueStats, error) { return natspkg.GetStreamStats(connArgs, queue) },
		},
		Extra: []*cobra.Command{
			mcp.NewCommand(mcp.Deps{
				ServerName:    "xmc-nats",
				ServerVersion: cmd.Version(),
				Target:        connArgs.Server,
				NewQueue: func() (backends.QueueBackend, error) {
					return natspkg.NewQueueAdapter(connArgs)
				},
				NewTopic: func() (backends.TopicBackend, error) {
					return natspkg.NewTopicAdapter(connArgs)
				},
				ListQueues: func(_ context.Context) ([]backends.QueueInfo, error) {
					nodes, err := natspkg.ListStreamsWithConsumers(connArgs)
					if err != nil {
						return nil, err
					}
					infos := make([]backends.QueueInfo, len(nodes))
					for i, n := range nodes {
						var msgs int64
						for _, m := range n.Metrics {
							if m.Label == "msgs" {
								msgs = m.Value
							}
						}
						infos[i] = backends.QueueInfo{Name: n.Name, MessageCount: msgs, ConsumerCount: len(n.Children)}
					}
					return infos, nil
				},
				PurgeQueue: func(_ context.Context, queue string) (int64, error) {
					return natspkg.PurgeStream(connArgs, queue)
				},
				QueueStats: func(_ context.Context, queue string) (*backends.QueueStats, error) {
					return natspkg.GetStreamStats(connArgs, queue)
				},
			}),
		},
	})
}
