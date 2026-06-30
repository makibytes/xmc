//go:build mqtt

package broker

import (
	"fmt"
	"os"

	"github.com/makibytes/xmc/broker/backends"
	"github.com/makibytes/xmc/broker/mqtt"
	"github.com/makibytes/xmc/cmd"
	"github.com/makibytes/xmc/mcp"
	"github.com/spf13/cobra"
)

func GetRootCommand() *cobra.Command {
	var connArgs mqtt.ConnArguments

	defaultServer := os.Getenv("MMC_SERVER")
	if defaultServer == "" {
		defaultServer = "tcp://localhost:1883"
	}

	var group string

	return cmd.NewRootCommand(cmd.BrokerSpec{
		Use:       "mmc",
		Short:     "MQTT Messaging Client",
		Long:      "Command-line interface for MQTT messaging",
		AIContext: AIDoc("mqtt"),
		ProduceFlags: func(c *cobra.Command) {
			c.Flags().Int("qos", 1, "QoS level (0, 1, or 2)")
			c.Flags().Bool("retain", false, "Set retain flag on published messages")
		},
		ProduceExtra: func(c *cobra.Command) map[string]string {
			extra := make(map[string]string)
			qos, _ := c.Flags().GetInt("qos")
			extra["qos"] = fmt.Sprintf("%d", qos)
			if r, _ := c.Flags().GetBool("retain"); r {
				extra["retain"] = "true"
			}
			return extra
		},
		ConsumeFlags: func(c *cobra.Command) {
			c.Flags().Int("qos", 1, "QoS level for subscription (0, 1, or 2)")
		},
		ConsumeExtra: func(c *cobra.Command) map[string]string {
			extra := make(map[string]string)
			qos, _ := c.Flags().GetInt("qos")
			extra["qos"] = fmt.Sprintf("%d", qos)
			if group != "" {
				extra["group"] = group
			}
			return extra
		},
		RegisterFlags: func(c *cobra.Command) {
			c.PersistentFlags().StringVarP(&connArgs.Server, "server", "s", defaultServer, "MQTT broker URL")
			c.PersistentFlags().StringVarP(&connArgs.User, "user", "u", os.Getenv("MMC_USER"), "Username")
			c.PersistentFlags().StringVarP(&connArgs.Password, "password", "p", os.Getenv("MMC_PASSWORD"), "Password")
			c.PersistentFlags().StringVar(&connArgs.ClientID, "client-id", "", "MQTT client ID (auto-generated if empty)")
			c.PersistentFlags().StringVar(&group, "group", "xmc", "Queue shared subscription group name")
			backends.RegisterTLSFlags(c, &connArgs.TLS)
		},
		Queue: func() (backends.QueueBackend, error) { return mqtt.NewQueueAdapter(connArgs) },
		Topic: func() (backends.TopicBackend, error) { return mqtt.NewTopicAdapter(connArgs) },
		Ping:  func() (cmd.Closeable, error) { return mqtt.NewQueueAdapter(connArgs) },
		Extra: []*cobra.Command{
			mcp.NewCommand(mcp.Deps{
				ServerName:    "xmc-mqtt",
				ServerVersion: cmd.Version(),
				Target:        connArgs.Server,
				NewQueue: func() (backends.QueueBackend, error) {
					return mqtt.NewQueueAdapter(connArgs)
				},
				NewTopic: func() (backends.TopicBackend, error) {
					return mqtt.NewTopicAdapter(connArgs)
				},
			}),
		},
	})
}
