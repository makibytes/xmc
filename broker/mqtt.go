//go:build mqtt

package broker

import (
	"os"

	"github.com/makibytes/xmc/broker/backends"
	"github.com/makibytes/xmc/broker/mqtt"
	"github.com/makibytes/xmc/cmd"
	"github.com/spf13/cobra"
)

func GetRootCommand() *cobra.Command {
	var connArgs mqtt.ConnArguments

	defaultServer := os.Getenv("MMC_SERVER")
	if defaultServer == "" {
		defaultServer = "tcp://localhost:1883"
	}

	return cmd.NewRootCommand(cmd.BrokerSpec{
		Use:   "mmc",
		Short: "MQTT Messaging Client",
		Long:  "Command-line interface for MQTT messaging",
		RegisterFlags: func(c *cobra.Command) {
			c.PersistentFlags().StringVarP(&connArgs.Server, "server", "s", defaultServer, "MQTT broker URL")
			c.PersistentFlags().StringVarP(&connArgs.User, "user", "u", os.Getenv("MMC_USER"), "Username")
			c.PersistentFlags().StringVarP(&connArgs.Password, "password", "p", os.Getenv("MMC_PASSWORD"), "Password")
			c.PersistentFlags().StringVar(&connArgs.ClientID, "client-id", "", "MQTT client ID (auto-generated if empty)")
			c.PersistentFlags().BoolVar(&connArgs.TLS.Enabled, "tls", false, "Enable TLS connection")
			c.PersistentFlags().StringVar(&connArgs.TLS.CACert, "ca-cert", "", "Path to CA certificate file")
			c.PersistentFlags().StringVar(&connArgs.TLS.ClientCert, "cert", "", "Path to client certificate file")
			c.PersistentFlags().StringVar(&connArgs.TLS.ClientKey, "key-file", "", "Path to client private key file")
			c.PersistentFlags().BoolVar(&connArgs.TLS.Insecure, "insecure", false, "Skip TLS certificate verification")
		},
		Queue: func() (backends.QueueBackend, error) { return mqtt.NewQueueAdapter(connArgs) },
		Topic: func() (backends.TopicBackend, error) { return mqtt.NewTopicAdapter(connArgs) },
		Ping:  func() (cmd.Closeable, error) { return mqtt.NewQueueAdapter(connArgs) },
	})
}
