//go:build rabbitmq

package broker

import (
	"os"

	"github.com/makibytes/xmc/broker/backends"
	"github.com/makibytes/xmc/broker/rabbitmq"
	"github.com/makibytes/xmc/cmd"
	"github.com/spf13/cobra"
)

func GetRootCommand() *cobra.Command {
	var connArgs rabbitmq.ConnArguments
	var exchange string

	defaultServer := os.Getenv("RMC_SERVER")
	if defaultServer == "" {
		defaultServer = "amqp://localhost:5672"
	}
	defaultExchange := os.Getenv("RMC_EXCHANGE")
	if defaultExchange == "" {
		defaultExchange = "amq.topic"
	}

	mgmtArgs := func() rabbitmq.ManagementArgs {
		return rabbitmq.ManagementArgs{Server: connArgs.Server, User: connArgs.User, Password: connArgs.Password}
	}

	return cmd.NewRootCommand(cmd.BrokerSpec{
		Use:   "rmc",
		Short: "RabbitMQ Messaging Client",
		Long:  "Command-line interface for RabbitMQ messaging (AMQP 1.0)",
		RegisterFlags: func(c *cobra.Command) {
			c.PersistentFlags().StringVarP(&connArgs.Server, "server", "s", defaultServer, "Server URL")
			c.PersistentFlags().StringVarP(&connArgs.User, "user", "u", os.Getenv("RMC_USER"), "Username for SASL PLAIN login")
			c.PersistentFlags().StringVarP(&connArgs.Password, "password", "p", os.Getenv("RMC_PASSWORD"), "Password for SASL PLAIN login")
			c.PersistentFlags().StringVarP(&exchange, "exchange", "e", defaultExchange, "Exchange name for topic operations")
			c.PersistentFlags().BoolVar(&connArgs.TLS.Enabled, "tls", false, "Enable TLS connection")
			c.PersistentFlags().StringVar(&connArgs.TLS.CACert, "ca-cert", "", "Path to CA certificate file")
			c.PersistentFlags().StringVar(&connArgs.TLS.ClientCert, "cert", "", "Path to client certificate file")
			c.PersistentFlags().StringVar(&connArgs.TLS.ClientKey, "key-file", "", "Path to client private key file")
			c.PersistentFlags().BoolVar(&connArgs.TLS.Insecure, "insecure", false, "Skip TLS certificate verification")
		},
		Queue: func() (backends.QueueBackend, error) { return rabbitmq.NewQueueAdapter(connArgs) },
		Topic: func() (backends.TopicBackend, error) { return rabbitmq.NewTopicAdapter(connArgs, exchange) },
		Ping:  func() (cmd.Closeable, error) { return rabbitmq.NewQueueAdapter(connArgs) },
		Manage: cmd.NewManageCommand(cmd.ManageSpec{
			ListQueues: func() ([]backends.QueueInfo, error) {
				queues, err := rabbitmq.ListQueues(mgmtArgs())
				if err != nil {
					return nil, err
				}
				out := make([]backends.QueueInfo, len(queues))
				for i, q := range queues {
					out[i] = backends.QueueInfo{Name: q.Name, MessageCount: q.MessageCount, ConsumerCount: q.ConsumerCount}
				}
				return out, nil
			},
			Purge: func(queue string) (int64, error) {
				return 0, rabbitmq.PurgeQueue(mgmtArgs(), queue)
			},
			Stats: func(queue string) (*backends.QueueStats, error) {
				stats, err := rabbitmq.GetQueueStats(mgmtArgs(), queue)
				if err != nil {
					return nil, err
				}
				return &backends.QueueStats{
					Name: stats.Name, MessageCount: stats.MessageCount,
					ConsumerCount: stats.ConsumerCount, EnqueueCount: stats.EnqueueCount,
					DequeueCount: stats.DequeueCount,
				}, nil
			},
		}),
	})
}
