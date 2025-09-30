//go:build ibmmq

package broker

import (
	"os"

	"github.com/makibytes/amc/broker/ibmmq"
	"github.com/makibytes/amc/cmd"
	"github.com/makibytes/amc/log"
	"github.com/spf13/cobra"
)

// GetRootCommand returns the IBM MQ root command
func GetRootCommand() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "amc",
		Short: "IBM MQ Messaging Client",
		Long:  "Command-line interface for IBM MQ messaging",
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			if verbose, _ := cmd.Flags().GetBool("verbose"); verbose {
				log.IsVerbose = true
			}
		},
	}

	// Connection flags
	var defaultServer = os.Getenv("AMC_SERVER")
	if defaultServer == "" {
		defaultServer = "ibmmq://localhost:1414/QM1?channel=SYSTEM.DEF.SVRCONN"
	}
	var defaultUser = os.Getenv("AMC_USER")
	var defaultPassword = os.Getenv("AMC_PASSWORD")
	var defaultQueueManager = os.Getenv("AMC_QUEUE_MANAGER")
	var defaultChannel = os.Getenv("AMC_CHANNEL")

	var connArgs ibmmq.ConnArguments

	rootCmd.PersistentFlags().StringVarP(&connArgs.Server, "server", "s", defaultServer, "Server URL")
	rootCmd.PersistentFlags().StringVarP(&connArgs.User, "user", "u", defaultUser, "Username for authentication")
	rootCmd.PersistentFlags().StringVarP(&connArgs.Password, "password", "p", defaultPassword, "Password for authentication")
	rootCmd.PersistentFlags().StringVarP(&connArgs.QueueManager, "qmgr", "m", defaultQueueManager, "Queue manager name (overrides URL)")
	rootCmd.PersistentFlags().StringVarP(&connArgs.Channel, "channel", "c", defaultChannel, "Channel name (overrides URL)")
	rootCmd.PersistentFlags().BoolP("verbose", "v", false, "Print verbose output")

	// Add commands - IBM MQ supports queues only
	sendCmd := cmd.NewSendCommand(nil)
	sendCmd.RunE = func(c *cobra.Command, args []string) error {
		adapter, err := ibmmq.NewQueueAdapter(connArgs)
		if err != nil {
			return err
		}
		defer adapter.Close()
		return cmd.NewSendCommand(adapter).RunE(c, args)
	}
	rootCmd.AddCommand(sendCmd)

	receiveCmd := cmd.NewReceiveCommand(nil)
	receiveCmd.RunE = func(c *cobra.Command, args []string) error {
		adapter, err := ibmmq.NewQueueAdapter(connArgs)
		if err != nil {
			return err
		}
		defer adapter.Close()
		return cmd.NewReceiveCommand(adapter).RunE(c, args)
	}
	rootCmd.AddCommand(receiveCmd)

	peekCmd := cmd.NewPeekCommand(nil)
	peekCmd.RunE = func(c *cobra.Command, args []string) error {
		adapter, err := ibmmq.NewQueueAdapter(connArgs)
		if err != nil {
			return err
		}
		defer adapter.Close()
		return cmd.NewPeekCommand(adapter).RunE(c, args)
	}
	rootCmd.AddCommand(peekCmd)

	return rootCmd
}