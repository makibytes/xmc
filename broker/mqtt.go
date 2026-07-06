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
	var mqttVersion int

	defaultVersion := 5
	if v := os.Getenv("MMC_MQTT_VERSION"); v == "3" || v == "4" {
		defaultVersion = 3
	}

	newQueue := func() (backends.QueueBackend, error) {
		switch mqttVersion {
		case 5:
			return mqtt.NewQueueAdapter(connArgs)
		case 3, 4: // 3.1.1 speaks protocol level 4; accept both spellings
			return mqtt.NewQueueAdapterV3(connArgs)
		default:
			return nil, fmt.Errorf("unsupported --mqtt-version %d (use 5 or 3)", mqttVersion)
		}
	}
	newTopic := func() (backends.TopicBackend, error) {
		switch mqttVersion {
		case 5:
			return mqtt.NewTopicAdapter(connArgs)
		case 3, 4:
			return mqtt.NewTopicAdapterV3(connArgs)
		default:
			return nil, fmt.Errorf("unsupported --mqtt-version %d (use 5 or 3)", mqttVersion)
		}
	}

	return cmd.NewRootCommand(cmd.BrokerSpec{
		Use:       "mmc",
		Short:     "MQTT Messaging Client",
		Long:      "Command-line interface for MQTT messaging",
		AIContext: AIDoc("mqtt"),
		// MQTT 5 (the default) carries properties and metadata natively;
		// --mqtt-version 3 rejects them at send time instead of warning here.
		UnsupportedFlags: []string{"priority", "selector"},
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
			// Named --queue-group (not --group) to avoid clashing with the
			// per-command -g/--group consumer-group flag on subscribe/forward/
			// bridge, which has a different meaning and default.
			c.PersistentFlags().StringVar(&group, "queue-group", "xmc", "Queue shared subscription group name")
			c.PersistentFlags().IntVar(&mqttVersion, "mqtt-version", defaultVersion, "MQTT protocol version: 5 (default) or 3 (legacy 3.1.1, no metadata support)")
			backends.RegisterTLSFlags(c, &connArgs.TLS)
		},
		Queue: newQueue,
		Topic: newTopic,
		Ping:  func() (cmd.Closeable, error) { return newQueue() },
		Extra: []*cobra.Command{
			mcp.NewCommand(mcp.Deps{
				ServerName:    "xmc-mqtt",
				ServerVersion: cmd.Version(),
				Target:        connArgs.Server,
				NewQueue: newQueue,
				NewTopic: newTopic,
			}),
		},
	})
}
