//go:build artemis

package broker

import (
	"context"
	"os"

	"github.com/makibytes/xmc/broker/artemis"
	"github.com/makibytes/xmc/broker/backends"
	"github.com/makibytes/xmc/cmd"
	"github.com/makibytes/xmc/mcp"
	"github.com/spf13/cobra"
)

func GetRootCommand() *cobra.Command {
	var connArgs artemis.ConnArguments

	defaultServer := os.Getenv("AMC_SERVER")
	if defaultServer == "" {
		defaultServer = "amqp://localhost:5672"
	}

	mgmtArgs := func() artemis.ManagementArgs {
		return artemis.ManagementArgs{Server: connArgs.Server, User: connArgs.User, Password: connArgs.Password}
	}

	return cmd.NewRootCommand(cmd.BrokerSpec{
		Use:   "amc",
		Short: "Apache Artemis Messaging Client",
		Long:  "Command-line interface for Apache Artemis messaging",
		RegisterFlags: func(c *cobra.Command) {
			c.PersistentFlags().StringVarP(&connArgs.Server, "server", "s", defaultServer, "Server URL")
			c.PersistentFlags().StringVarP(&connArgs.User, "user", "u", os.Getenv("AMC_USER"), "Username for SASL PLAIN login")
			c.PersistentFlags().StringVarP(&connArgs.Password, "password", "p", os.Getenv("AMC_PASSWORD"), "Password for SASL PLAIN login")
			c.PersistentFlags().BoolVar(&connArgs.TLS.Enabled, "tls", false, "Enable TLS connection")
			c.PersistentFlags().StringVar(&connArgs.TLS.CACert, "ca-cert", "", "Path to CA certificate file")
			c.PersistentFlags().StringVar(&connArgs.TLS.ClientCert, "cert", "", "Path to client certificate file")
			c.PersistentFlags().StringVar(&connArgs.TLS.ClientKey, "key-file", "", "Path to client private key file")
			c.PersistentFlags().BoolVar(&connArgs.TLS.Insecure, "insecure", false, "Skip TLS certificate verification")
		},
		Queue: func() (backends.QueueBackend, error) { return artemis.NewQueueAdapter(connArgs) },
		Topic: func() (backends.TopicBackend, error) { return artemis.NewTopicAdapter(connArgs) },
		Ping:  func() (cmd.Closeable, error) { return artemis.NewQueueAdapter(connArgs) },
		Manage: cmd.NewManageCommand(cmd.ManageSpec{
			ListQueues: func() ([]backends.QueueInfo, error) {
				queues, err := artemis.ListQueues(mgmtArgs())
				if err != nil {
					return nil, err
				}
				out := make([]backends.QueueInfo, len(queues))
				for i, q := range queues {
					out[i] = backends.QueueInfo{Name: q.Name, MessageCount: q.MessageCount, ConsumerCount: q.ConsumerCount}
				}
				return out, nil
			},
			Purge: func(queue string) (int64, error) { return artemis.PurgeQueue(mgmtArgs(), queue) },
			Stats: func(queue string) (*backends.QueueStats, error) {
				stats, err := artemis.GetQueueStats(mgmtArgs(), queue)
				if err != nil {
					return nil, err
				}
				return &backends.QueueStats{
					Name: stats.Name, MessageCount: stats.MessageCount,
					ConsumerCount: stats.ConsumerCount, EnqueueCount: stats.EnqueueCount,
					DequeueCount: stats.DequeueCount,
				}, nil
			},
			CreateQueue: &cmd.ManageAction{Run: func(queue string) error { return artemis.CreateQueue(mgmtArgs(), queue) }},
			DeleteQueue: &cmd.ManageAction{Run: func(queue string) error { return artemis.DeleteQueue(mgmtArgs(), queue) }},
			CreateTopic: &cmd.ManageAction{Run: func(topic string) error { return artemis.CreateTopic(mgmtArgs(), topic) }},
			DeleteTopic: &cmd.ManageAction{Run: func(topic string) error { return artemis.DeleteTopic(mgmtArgs(), topic) }},
		}),
		Extra: []*cobra.Command{
			mcp.NewCommand(mcp.Deps{
				ServerName:    "xmc-artemis",
				ServerVersion: cmd.Version(),
				Target:        connArgs.Server,
				NewQueue: func() (backends.QueueBackend, error) {
					return artemis.NewQueueAdapter(connArgs)
				},
				NewTopic: func() (backends.TopicBackend, error) {
					return artemis.NewTopicAdapter(connArgs)
				},
				ListQueues: func(ctx context.Context) ([]mcp.QueueInfo, error) {
					queues, err := artemis.ListQueues(mgmtArgs())
					if err != nil {
						return nil, err
					}
					out := make([]mcp.QueueInfo, 0, len(queues))
					for _, q := range queues {
						out = append(out, mcp.QueueInfo{
							Name: q.Name, RoutingType: q.RoutingType,
							MessageCount: int64(q.MessageCount), ConsumerCount: int64(q.ConsumerCount),
						})
					}
					return out, nil
				},
				PurgeQueue: func(ctx context.Context, queue string) (int64, error) {
					return artemis.PurgeQueue(mgmtArgs(), queue)
				},
				QueueStats: func(ctx context.Context, queue string) (*mcp.QueueStats, error) {
					stats, err := artemis.GetQueueStats(mgmtArgs(), queue)
					if err != nil {
						return nil, err
					}
					return &mcp.QueueStats{
						Name: stats.Name, MessageCount: int64(stats.MessageCount),
						ConsumerCount: int64(stats.ConsumerCount), EnqueueCount: int64(stats.EnqueueCount),
						DequeueCount: int64(stats.DequeueCount),
					}, nil
				},
			}),
		},
	})
}
