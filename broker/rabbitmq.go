//go:build rabbitmq

package broker

import (
	"context"
	"os"

	"github.com/makibytes/xmc/broker/backends"
	"github.com/makibytes/xmc/broker/rabbitmq"
	"github.com/makibytes/xmc/cmd"
	"github.com/makibytes/xmc/mcp"
	"github.com/spf13/cobra"
)

func GetRootCommand() *cobra.Command {
	var connArgs rabbitmq.ConnArguments
	var exchangeType string
	var routingKey string

	defaultServer := os.Getenv("RMC_SERVER")
	if defaultServer == "" {
		defaultServer = "amqp://localhost:5672"
	}

	mgmtArgs := func() rabbitmq.ManagementArgs {
		return rabbitmq.ManagementArgs{Server: connArgs.Server, User: connArgs.User, Password: connArgs.Password}
	}

	return cmd.NewRootCommand(cmd.BrokerSpec{
		Use:       "rmc",
		Short:     "RabbitMQ Messaging Client",
		Long:      "Command-line interface for RabbitMQ messaging (AMQP 1.0)",
		AIContext: AIDoc("rabbitmq"),
		ResolveTarget: func(t cmd.TargetSpec) (string, error) {
			return rabbitmq.ResolveTarget(t.IsTopic, t.To, t.Exchange, t.Queue)
		},
		ExchangeRouting: true,
		RegisterFlags: func(c *cobra.Command) {
			backends.RegisterCommonFlags(c, &connArgs.Server, &connArgs.User, &connArgs.Password, "RMC_", defaultServer,
				"Server URL", "Username for SASL PLAIN login", "Password for SASL PLAIN login")
			backends.RegisterTLSFlags(c, &connArgs.TLS)
		},
		Queue: func() (backends.QueueBackend, error) { return rabbitmq.NewQueueAdapter(connArgs) },
		Topic: func() (backends.TopicBackend, error) { return rabbitmq.NewTopicAdapter(connArgs) },
		Ping:  func() (cmd.Closeable, error) { return rabbitmq.NewQueueAdapter(connArgs) },
		ManageSpec: &cmd.ManageSpec{
			Objects: []cmd.ObjectType{
				{
					Label: "Queues",
					Drain: true,
					List: func() ([]backends.ObjectNode, error) {
						queues, err := rabbitmq.ListQueues(mgmtArgs())
						if err != nil {
							return nil, err
						}
						out := make([]backends.ObjectNode, len(queues))
						for i, q := range queues {
							out[i] = backends.ObjectNode{
								Name:    q.Name,
								Metrics: []backends.Metric{{Label: "msgs", Value: q.MessageCount}, {Label: "consumers", Value: int64(q.ConsumerCount)}},
							}
						}
						return out, nil
					},
				},
				{
					// An exchange routes to whatever queues its bindings
					// name, not a same-named queue, so Drain stays false —
					// purge/peek/receive by exchange name would silently
					// target the wrong queue (or nothing). Send always
					// dispatches via the topic adapter (an exchange is
					// always routing-only, unlike an Artemis address which
					// may be pure ANYCAST).
					Label:        "Exchanges",
					Hierarchical: true,
					SendViaTopic: func(string) bool { return true },
					List: func() ([]backends.ObjectNode, error) {
						return rabbitmq.ListExchanges(mgmtArgs())
					},
				},
			},
			Purge: func(queue string) (int64, error) {
				return rabbitmq.PurgeQueue(mgmtArgs(), queue)
			},
			Stats: func(queue string) (*backends.QueueStats, error) {
				return rabbitmq.GetQueueStats(mgmtArgs(), queue)
			},
			CreateQueue: &cmd.ManageAction{Run: func(queue string) error { return rabbitmq.CreateQueue(mgmtArgs(), queue) }},
			DeleteQueue: &cmd.ManageAction{Run: func(queue string) error { return rabbitmq.DeleteQueue(mgmtArgs(), queue) }},
			CreateExchange: &cmd.ManageAction{
				SetupFlags: func(c *cobra.Command) {
					c.Flags().StringVar(&exchangeType, "type", "topic", "Exchange type (direct, fanout, topic, headers)")
				},
				Run: func(name string) error { return rabbitmq.CreateExchange(mgmtArgs(), name, exchangeType) },
			},
			DeleteExchange: &cmd.ManageAction{Run: func(name string) error { return rabbitmq.DeleteExchange(mgmtArgs(), name) }},
			BindQueue: &cmd.BindAction{
				SetupFlags: func(c *cobra.Command) {
					c.Flags().StringVar(&routingKey, "routing-key", "#", "Routing key for the binding")
				},
				Run: func(queue, exchange string) error { return rabbitmq.BindQueue(mgmtArgs(), queue, exchange, routingKey) },
			},
			UnbindQueue: &cmd.BindAction{
				SetupFlags: func(c *cobra.Command) {
					c.Flags().StringVar(&routingKey, "routing-key", "#", "Routing key for the binding to remove")
				},
				Run: func(queue, exchange string) error {
					return rabbitmq.UnbindQueue(mgmtArgs(), queue, exchange, routingKey)
				},
			},
		},
		Extra: []*cobra.Command{
			mcp.NewCommand(mcp.Deps{
				ServerName:    "xmc-rabbitmq",
				ServerVersion: cmd.Version(),
				Target:        connArgs.Server,
				NewQueue: func() (backends.QueueBackend, error) {
					return rabbitmq.NewQueueAdapter(connArgs)
				},
				NewTopic: func() (backends.TopicBackend, error) {
					return rabbitmq.NewTopicAdapter(connArgs)
				},
				ListQueues: func(_ context.Context) ([]backends.QueueInfo, error) {
					return rabbitmq.ListQueues(mgmtArgs())
				},
				PurgeQueue: func(_ context.Context, queue string) (int64, error) {
					return rabbitmq.PurgeQueue(mgmtArgs(), queue)
				},
				QueueStats: func(_ context.Context, queue string) (*backends.QueueStats, error) {
					return rabbitmq.GetQueueStats(mgmtArgs(), queue)
				},
			}),
		},
	})
}
