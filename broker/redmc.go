//go:build redis

package broker

import (
	"os"

	"github.com/makibytes/xmc/broker/backends"
	redispkg "github.com/makibytes/xmc/broker/redis"
	"github.com/makibytes/xmc/cmd"
	"github.com/spf13/cobra"
)

func GetRootCommand() *cobra.Command {
	var connArgs redispkg.ConnArguments

	defaultServer := os.Getenv("REDMC_SERVER")
	if defaultServer == "" {
		defaultServer = "redis://localhost:6379"
	}

	return cmd.NewRootCommand(cmd.BrokerSpec{
		Use:   "redmc",
		Short: "Redis Messaging Client",
		Long:  "Command-line interface for Redis messaging (Streams)",
		RegisterFlags: func(c *cobra.Command) {
			c.PersistentFlags().StringVarP(&connArgs.Server, "server", "s", defaultServer, "Server URL")
			c.PersistentFlags().StringVarP(&connArgs.User, "user", "u", os.Getenv("REDMC_USER"), "Username for authentication")
			c.PersistentFlags().StringVarP(&connArgs.Password, "password", "p", os.Getenv("REDMC_PASSWORD"), "Password for authentication")
			c.PersistentFlags().BoolVar(&connArgs.TLS.Enabled, "tls", false, "Enable TLS connection")
			c.PersistentFlags().StringVar(&connArgs.TLS.CACert, "ca-cert", "", "Path to CA certificate file")
			c.PersistentFlags().StringVar(&connArgs.TLS.ClientCert, "cert", "", "Path to client certificate file")
			c.PersistentFlags().StringVar(&connArgs.TLS.ClientKey, "key-file", "", "Path to client private key file")
			c.PersistentFlags().BoolVar(&connArgs.TLS.Insecure, "insecure", false, "Skip TLS certificate verification")
		},
		Queue: func() (backends.QueueBackend, error) { return redispkg.NewQueueAdapter(connArgs) },
		Topic: func() (backends.TopicBackend, error) { return redispkg.NewTopicAdapter(connArgs) },
		Ping:  func() (cmd.Closeable, error) { return redispkg.NewQueueAdapter(connArgs) },
		Manage: cmd.NewManageCommand(cmd.ManageSpec{
			ListQueues:  func() ([]backends.QueueInfo, error) { return redispkg.ListQueues(connArgs) },
			ListTopics:  func() ([]backends.TopicInfo, error) { return redispkg.ListTopics(connArgs) },
			Purge:       func(queue string) (int64, error) { return redispkg.PurgeQueue(connArgs, queue) },
			Stats:       func(queue string) (*backends.QueueStats, error) { return redispkg.GetQueueStats(connArgs, queue) },
			CreateQueue: &cmd.ManageAction{Run: func(queue string) error { return redispkg.CreateQueue(connArgs, queue) }},
			DeleteQueue: &cmd.ManageAction{Run: func(queue string) error { return redispkg.DeleteQueue(connArgs, queue) }},
			CreateTopic: &cmd.ManageAction{Run: func(topic string) error { return redispkg.CreateTopic(connArgs, topic) }},
			DeleteTopic: &cmd.ManageAction{Run: func(topic string) error { return redispkg.DeleteTopic(connArgs, topic) }},
		}),
	})
}
