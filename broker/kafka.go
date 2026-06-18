//go:build kafka

package broker

import (
	"fmt"
	"os"
	"strings"

	"github.com/makibytes/xmc/broker/backends"
	"github.com/makibytes/xmc/broker/kafka"
	"github.com/makibytes/xmc/cmd"
	"github.com/spf13/cobra"
)

func GetRootCommand() *cobra.Command {
	var connArgs kafka.ConnArguments
	var partitions int
	var replicationFactor int
	var configEntries []string

	defaultServer := os.Getenv("KMC_SERVER")
	if defaultServer == "" {
		defaultServer = "kafka://localhost:9092"
	}

	topicFactory := cmd.TopicAdapterFactory(func() (backends.TopicBackend, error) {
		return kafka.NewTopicAdapter(connArgs)
	})

	return cmd.NewRootCommand(cmd.BrokerSpec{
		Use:   "kmc",
		Short: "Apache Kafka Messaging Client",
		Long:  "Command-line interface for Apache Kafka messaging",
		RegisterFlags: func(c *cobra.Command) {
			c.PersistentFlags().StringVarP(&connArgs.Server, "server", "s", defaultServer, "Server URL (kafka://broker1:9092 or kafka://broker1:9092,broker2:9092)")
			c.PersistentFlags().StringVarP(&connArgs.User, "user", "u", os.Getenv("KMC_USER"), "Username for SASL authentication")
			c.PersistentFlags().StringVarP(&connArgs.Password, "password", "p", os.Getenv("KMC_PASSWORD"), "Password for SASL authentication")
			c.PersistentFlags().BoolVar(&connArgs.TLS.Enabled, "tls", false, "Enable TLS connection")
			c.PersistentFlags().StringVar(&connArgs.TLS.CACert, "ca-cert", "", "Path to CA certificate file")
			c.PersistentFlags().StringVar(&connArgs.TLS.ClientCert, "cert", "", "Path to client certificate file")
			c.PersistentFlags().StringVar(&connArgs.TLS.ClientKey, "key-file", "", "Path to client private key file")
			c.PersistentFlags().BoolVar(&connArgs.TLS.Insecure, "insecure", false, "Skip TLS certificate verification")
		},
		Topic: topicFactory,
		Ping:  func() (cmd.Closeable, error) { return kafka.NewTopicAdapter(connArgs) },
		Manage: cmd.NewManageCommand(cmd.ManageSpec{
			ListTopics: func() ([]backends.TopicInfo, error) {
				topics, err := kafka.ListTopics(connArgs)
				if err != nil {
					return nil, err
				}
				out := make([]backends.TopicInfo, len(topics))
				for i, t := range topics {
					out[i] = backends.TopicInfo{Name: t.Name, PartitionCount: t.PartitionCount}
				}
				return out, nil
			},
			CreateTopic: &cmd.ManageAction{
				SetupFlags: func(c *cobra.Command) {
					c.Flags().IntVar(&partitions, "partitions", 1, "Number of partitions")
					c.Flags().IntVar(&replicationFactor, "replication-factor", 1, "Replication factor")
					c.Flags().StringArrayVar(&configEntries, "config", nil, "Topic config entries (key=value, repeatable)")
				},
				Run: func(topic string) error {
					configs := make(map[string]string)
					for _, entry := range configEntries {
						k, v, ok := strings.Cut(entry, "=")
						if !ok {
							return fmt.Errorf("invalid config entry %q (expected key=value)", entry)
						}
						configs[k] = v
					}
					return kafka.CreateTopic(connArgs, topic, partitions, replicationFactor, configs)
				},
			},
			DeleteTopic: &cmd.ManageAction{Run: func(topic string) error { return kafka.DeleteTopic(connArgs, topic) }},
		}),
		Extra: []*cobra.Command{
			cmd.WrapTopicCommand(cmd.NewForwardTopicCommand, topicFactory),
		},
	})
}
