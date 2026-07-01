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
		Use:       "kmc",
		Short:     "Apache Kafka Messaging Client",
		Long:      "Command-line interface for Apache Kafka messaging",
		AIContext: AIDoc("kafka"),
		ConsumeFlags: func(c *cobra.Command) {
			c.Flags().Int("partition", -1, "Read from a specific partition (disables consumer group)")
			c.Flags().String("offset", "", "Start offset: earliest, latest, or a number (requires --partition)")
		},
		ConsumeExtra: func(c *cobra.Command) map[string]string {
			extra := make(map[string]string)
			if p, _ := c.Flags().GetInt("partition"); p >= 0 {
				extra["partition"] = fmt.Sprintf("%d", p)
			}
			if o, _ := c.Flags().GetString("offset"); o != "" {
				extra["offset"] = o
			}
			return extra
		},
		RegisterFlags: func(c *cobra.Command) {
			c.PersistentFlags().StringVarP(&connArgs.Server, "server", "s", defaultServer, "Server URL (kafka://broker1:9092 or kafka://broker1:9092,broker2:9092)")
			c.PersistentFlags().StringVarP(&connArgs.User, "user", "u", os.Getenv("KMC_USER"), "Username for SASL authentication")
			c.PersistentFlags().StringVarP(&connArgs.Password, "password", "p", os.Getenv("KMC_PASSWORD"), "Password for SASL authentication")
			backends.RegisterTLSFlags(c, &connArgs.TLS)
		},
		Topic: topicFactory,
		Ping:  func() (cmd.Closeable, error) { return kafka.NewTopicAdapter(connArgs) },
		ManageSpec: &cmd.ManageSpec{
			Objects: []cmd.ObjectType{
				{
					Label: "Topics",
					List: func() ([]backends.ObjectNode, error) {
						topics, err := kafka.ListTopics(connArgs)
						if err != nil {
							return nil, err
						}
						out := make([]backends.ObjectNode, len(topics))
						for i, t := range topics {
							out[i] = backends.ObjectNode{
								Name:    t.Name,
								Metrics: []backends.Metric{{Label: "partitions", Value: int64(t.PartitionCount)}},
							}
						}
						return out, nil
					},
				},
				{
					Label: "Consumer Groups",
					List: func() ([]backends.ObjectNode, error) {
						return kafka.ListConsumerGroups(connArgs)
					},
				},
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
		},
		Extra: []*cobra.Command{
			cmd.WrapTopicCommand(cmd.NewForwardTopicCommand, topicFactory),
			cmd.WrapTopicCommand(cmd.NewBridgeTopicCommand, topicFactory),
		},
	})
}
