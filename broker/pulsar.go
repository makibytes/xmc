//go:build pulsar

package broker

import (
	"os"

	"github.com/makibytes/xmc/broker/backends"
	pulsarpkg "github.com/makibytes/xmc/broker/pulsar"
	"github.com/makibytes/xmc/cmd"
	"github.com/spf13/cobra"
)

func GetRootCommand() *cobra.Command {
	var connArgs pulsarpkg.ConnArguments
	var adminPort int
	var pulsarPartitions int
	var tenant, namespace string
	var nonPersistent bool

	defaultServer := os.Getenv("PMC_SERVER")
	if defaultServer == "" {
		defaultServer = "pulsar://localhost:6650"
	}

	return cmd.NewRootCommand(cmd.BrokerSpec{
		Use:       "pmc",
		Short:     "Pulsar Messaging Client",
		Long:      "Command-line interface for Apache Pulsar messaging",
		AIContext: AIDoc("pulsar"),
		ResolveTarget: func(t cmd.TargetSpec) (string, error) {
			return pulsarpkg.ResolveTarget(t.IsTopic, t.To, tenant, namespace, nonPersistent)
		},
		RegisterFlags: func(c *cobra.Command) {
			c.PersistentFlags().StringVarP(&connArgs.Server, "server", "s", defaultServer, "Server URL")
			c.PersistentFlags().StringVarP(&connArgs.User, "user", "u", os.Getenv("PMC_USER"), "Username for authentication")
			c.PersistentFlags().StringVarP(&connArgs.Password, "password", "p", os.Getenv("PMC_PASSWORD"), "Password for authentication")
			c.PersistentFlags().StringVar(&tenant, "tenant", "public", "Pulsar tenant")
			c.PersistentFlags().StringVar(&namespace, "namespace", "default", "Pulsar namespace")
			c.PersistentFlags().BoolVar(&nonPersistent, "non-persistent", false, "Use non-persistent topics")
			c.PersistentFlags().BoolVar(&connArgs.TLS.Enabled, "tls", false, "Enable TLS connection")
			c.PersistentFlags().StringVar(&connArgs.TLS.CACert, "ca-cert", "", "Path to CA certificate file")
			c.PersistentFlags().StringVar(&connArgs.TLS.ClientCert, "cert", "", "Path to client certificate file")
			c.PersistentFlags().StringVar(&connArgs.TLS.ClientKey, "key-file", "", "Path to client private key file")
			c.PersistentFlags().BoolVar(&connArgs.TLS.Insecure, "insecure", false, "Skip TLS certificate verification")
		},
		Queue: func() (backends.QueueBackend, error) { return pulsarpkg.NewQueueAdapter(connArgs) },
		Topic: func() (backends.TopicBackend, error) { return pulsarpkg.NewTopicAdapter(connArgs) },
		Ping:  func() (cmd.Closeable, error) { return pulsarpkg.NewQueueAdapter(connArgs) },
		ManageSpec: &cmd.ManageSpec{
			SetupFlags: func(c *cobra.Command) {
				c.PersistentFlags().IntVar(&adminPort, "admin-port", 8080, "Pulsar admin REST API port")
			},
			Objects: []cmd.ObjectType{
				{
					Label: "Topics",
					List: func() ([]backends.ObjectNode, error) {
						topics, err := pulsarpkg.ListTopics(connArgs, adminPort, tenant, namespace, nonPersistent)
						if err != nil {
							return nil, err
						}
						out := make([]backends.ObjectNode, len(topics))
						kind := "persistent"
						if nonPersistent {
							kind = "non-persist"
						}
						for i, t := range topics {
							out[i] = backends.ObjectNode{Name: t.Name, Kind: kind}
						}
						return out, nil
					},
				},
			},
			CreateTopic: &cmd.ManageAction{
				SetupFlags: func(c *cobra.Command) {
					c.Flags().IntVar(&pulsarPartitions, "partitions", 0, "Number of partitions (0 = non-partitioned)")
				},
				Run: func(topic string) error {
					return pulsarpkg.CreateTopic(connArgs, adminPort, topic, tenant, namespace, nonPersistent, pulsarPartitions)
				},
			},
			DeleteTopic: &cmd.ManageAction{Run: func(topic string) error {
				return pulsarpkg.DeleteTopic(connArgs, adminPort, topic, tenant, namespace, nonPersistent)
			}},
		},
	})
}
