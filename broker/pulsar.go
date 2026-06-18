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

	defaultServer := os.Getenv("PMC_SERVER")
	if defaultServer == "" {
		defaultServer = "pulsar://localhost:6650"
	}

	return cmd.NewRootCommand(cmd.BrokerSpec{
		Use:   "pmc",
		Short: "Pulsar Messaging Client",
		Long:  "Command-line interface for Apache Pulsar messaging",
		RegisterFlags: func(c *cobra.Command) {
			c.PersistentFlags().StringVarP(&connArgs.Server, "server", "s", defaultServer, "Server URL")
			c.PersistentFlags().StringVarP(&connArgs.User, "user", "u", os.Getenv("PMC_USER"), "Username for authentication")
			c.PersistentFlags().StringVarP(&connArgs.Password, "password", "p", os.Getenv("PMC_PASSWORD"), "Password for authentication")
			c.PersistentFlags().BoolVar(&connArgs.TLS.Enabled, "tls", false, "Enable TLS connection")
			c.PersistentFlags().StringVar(&connArgs.TLS.CACert, "ca-cert", "", "Path to CA certificate file")
			c.PersistentFlags().StringVar(&connArgs.TLS.ClientCert, "cert", "", "Path to client certificate file")
			c.PersistentFlags().StringVar(&connArgs.TLS.ClientKey, "key-file", "", "Path to client private key file")
			c.PersistentFlags().BoolVar(&connArgs.TLS.Insecure, "insecure", false, "Skip TLS certificate verification")
		},
		Queue: func() (backends.QueueBackend, error) { return pulsarpkg.NewQueueAdapter(connArgs) },
		Topic: func() (backends.TopicBackend, error) { return pulsarpkg.NewTopicAdapter(connArgs) },
		Ping:  func() (cmd.Closeable, error) { return pulsarpkg.NewQueueAdapter(connArgs) },
		Manage: cmd.NewManageCommand(cmd.ManageSpec{
			SetupFlags: func(c *cobra.Command) {
				c.PersistentFlags().IntVar(&adminPort, "admin-port", 8080, "Pulsar admin REST API port")
			},
			ListTopics: func() ([]backends.TopicInfo, error) {
				topics, err := pulsarpkg.ListTopics(connArgs, adminPort)
				if err != nil {
					return nil, err
				}
				out := make([]backends.TopicInfo, len(topics))
				for i, t := range topics {
					out[i] = backends.TopicInfo{Name: t.Name}
				}
				return out, nil
			},
		}),
	})
}
