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
	var prefix string
	var maxLen int64

	defaultServer := os.Getenv("REDMC_SERVER")
	if defaultServer == "" {
		defaultServer = "redis://localhost:6379"
	}

	return cmd.NewRootCommand(cmd.BrokerSpec{
		Use:       "redmc",
		Short:     "Redis Messaging Client",
		Long:      "Command-line interface for Redis messaging (Streams)",
		AIContext: AIDoc("redis"),
		ResolveTarget: func(t cmd.TargetSpec) (string, error) {
			return redispkg.ResolveTarget(t.IsTopic, t.To, prefix)
		},
		RegisterFlags: func(c *cobra.Command) {
			c.PersistentFlags().StringVarP(&connArgs.Server, "server", "s", defaultServer, "Server URL")
			c.PersistentFlags().StringVarP(&connArgs.User, "user", "u", os.Getenv("REDMC_USER"), "Username for authentication")
			c.PersistentFlags().StringVarP(&connArgs.Password, "password", "p", os.Getenv("REDMC_PASSWORD"), "Password for authentication")
			c.PersistentFlags().StringVar(&prefix, "prefix", "xmc", "Key prefix for Redis streams")
			c.PersistentFlags().Int64Var(&maxLen, "maxlen", 10000, "Maximum topic stream length (0 = no trim)")
			c.PersistentFlags().BoolVar(&connArgs.TLS.Enabled, "tls", false, "Enable TLS connection")
			c.PersistentFlags().StringVar(&connArgs.TLS.CACert, "ca-cert", "", "Path to CA certificate file")
			c.PersistentFlags().StringVar(&connArgs.TLS.ClientCert, "cert", "", "Path to client certificate file")
			c.PersistentFlags().StringVar(&connArgs.TLS.ClientKey, "key-file", "", "Path to client private key file")
			c.PersistentFlags().BoolVar(&connArgs.TLS.Insecure, "insecure", false, "Skip TLS certificate verification")
		},
		Queue: func() (backends.QueueBackend, error) { return redispkg.NewQueueAdapter(connArgs) },
		Topic: func() (backends.TopicBackend, error) { return redispkg.NewTopicAdapter(connArgs, maxLen) },
		Ping:  func() (cmd.Closeable, error) { return redispkg.NewQueueAdapter(connArgs) },
		ManageSpec: &cmd.ManageSpec{
			Objects: []cmd.ObjectType{
				{
					Label: "Queues",
					List: func() ([]backends.ObjectNode, error) {
						queues, err := redispkg.ListQueues(connArgs, prefix)
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
					Label: "Topics",
					List: func() ([]backends.ObjectNode, error) {
						topics, err := redispkg.ListTopics(connArgs, prefix)
						if err != nil {
							return nil, err
						}
						out := make([]backends.ObjectNode, len(topics))
						for i, t := range topics {
							out[i] = backends.ObjectNode{Name: t.Name}
						}
						return out, nil
					},
				},
			},
			Purge:       func(queue string) (int64, error) { return redispkg.PurgeQueue(connArgs, prefix, queue) },
			Stats:       func(queue string) (*backends.QueueStats, error) { return redispkg.GetQueueStats(connArgs, prefix, queue) },
			CreateQueue: &cmd.ManageAction{Run: func(queue string) error { return redispkg.CreateQueue(connArgs, prefix, queue) }},
			DeleteQueue: &cmd.ManageAction{Run: func(queue string) error { return redispkg.DeleteQueue(connArgs, prefix, queue) }},
			CreateTopic: &cmd.ManageAction{Run: func(topic string) error { return redispkg.CreateTopic(connArgs, prefix, topic) }},
			DeleteTopic: &cmd.ManageAction{Run: func(topic string) error { return redispkg.DeleteTopic(connArgs, prefix, topic) }},
		},
	})
}
