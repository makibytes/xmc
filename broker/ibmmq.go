//go:build ibmmq

package broker

import (
	"os"

	"github.com/makibytes/xmc/broker/backends"
	"github.com/makibytes/xmc/broker/ibmmq"
	"github.com/makibytes/xmc/cmd"
	"github.com/makibytes/xmc/log"
	"github.com/spf13/cobra"
)

// GetRootCommand returns the IBM MQ root command
func GetRootCommand() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "imc",
		Short: "IBM MQ Messaging Client",
		Long:  "Command-line interface for IBM MQ messaging",
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			if verbose, _ := cmd.Flags().GetBool("verbose"); verbose {
				log.IsVerbose = true
			}
		},
	}

	// Connection flags
	var defaultServer = os.Getenv("IMC_SERVER")
	if defaultServer == "" {
		defaultServer = "ibmmq://localhost:1414/QM1?channel=SYSTEM.DEF.SVRCONN"
	}
	var defaultUser = os.Getenv("IMC_USER")
	var defaultPassword = os.Getenv("IMC_PASSWORD")
	var defaultQueueManager = os.Getenv("IMC_QUEUE_MANAGER")
	var defaultChannel = os.Getenv("IMC_CHANNEL")

	var connArgs ibmmq.ConnArguments

	rootCmd.PersistentFlags().StringVarP(&connArgs.Server, "server", "s", defaultServer, "Server URL")
	rootCmd.PersistentFlags().StringVarP(&connArgs.User, "user", "u", defaultUser, "Username for authentication")
	rootCmd.PersistentFlags().StringVarP(&connArgs.Password, "password", "p", defaultPassword, "Password for authentication")
	rootCmd.PersistentFlags().StringVarP(&connArgs.QueueManager, "qmgr", "m", defaultQueueManager, "Queue manager name (overrides URL)")
	rootCmd.PersistentFlags().StringVarP(&connArgs.Channel, "channel", "c", defaultChannel, "Channel name (overrides URL)")
	rootCmd.PersistentFlags().BoolP("verbose", "v", false, "Print verbose output")

	// Queue commands (IBM MQ is queue-only)
	queueFactory := cmd.QueueAdapterFactory(func() (backends.QueueBackend, error) {
		return ibmmq.NewQueueAdapter(connArgs)
	})
	rootCmd.AddCommand(cmd.WrapQueueCommand(cmd.NewSendCommand, queueFactory))
	rootCmd.AddCommand(cmd.WrapQueueCommand(cmd.NewReceiveCommand, queueFactory))
	rootCmd.AddCommand(cmd.WrapQueueCommand(cmd.NewPeekCommand, queueFactory))
	rootCmd.AddCommand(cmd.WrapQueueCommand(cmd.NewRequestCommand, queueFactory))

	// Version command
	rootCmd.AddCommand(cmd.NewVersionCommand())

	return rootCmd
}
