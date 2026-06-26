//go:build ibmmq

package broker

import (
	"os"

	"github.com/makibytes/xmc/broker/backends"
	"github.com/makibytes/xmc/broker/ibmmq"
	"github.com/makibytes/xmc/cmd"
	"github.com/makibytes/xmc/mcp"
	"github.com/spf13/cobra"
)

func GetRootCommand() *cobra.Command {
	var connArgs ibmmq.ConnArguments

	defaultServer := os.Getenv("IMC_SERVER")
	if defaultServer == "" {
		defaultServer = "ibmmq://localhost:1414/QM1?channel=SYSTEM.DEF.SVRCONN"
	}

	return cmd.NewRootCommand(cmd.BrokerSpec{
		Use:       "imc",
		Short:     "IBM MQ Messaging Client",
		Long:      "Command-line interface for IBM MQ messaging",
		AIContext: AIDoc("ibmmq"),
		RegisterFlags: func(c *cobra.Command) {
			c.PersistentFlags().StringVarP(&connArgs.Server, "server", "s", defaultServer, "Server URL")
			c.PersistentFlags().StringVarP(&connArgs.User, "user", "u", os.Getenv("IMC_USER"), "Username for authentication")
			c.PersistentFlags().StringVarP(&connArgs.Password, "password", "p", os.Getenv("IMC_PASSWORD"), "Password for authentication")
			c.PersistentFlags().StringVarP(&connArgs.QueueManager, "qmgr", "m", os.Getenv("IMC_QUEUE_MANAGER"), "Queue manager name (overrides URL)")
			c.PersistentFlags().StringVarP(&connArgs.Channel, "channel", "c", os.Getenv("IMC_CHANNEL"), "Channel name (overrides URL)")
		},
		Queue: func() (backends.QueueBackend, error) { return ibmmq.NewQueueAdapter(connArgs) },
		Ping:  func() (cmd.Closeable, error) { return ibmmq.NewQueueAdapter(connArgs) },
		Extra: []*cobra.Command{
			mcp.NewCommand(mcp.Deps{
				ServerName:    "xmc-ibmmq",
				ServerVersion: cmd.Version(),
				Target:        connArgs.Server,
				NewQueue: func() (backends.QueueBackend, error) {
					return ibmmq.NewQueueAdapter(connArgs)
				},
			}),
		},
	})
}
