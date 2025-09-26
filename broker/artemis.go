//go:build artemis

package broker

import (
	"os"

	"github.com/makibytes/amc/broker/artemis"
	"github.com/makibytes/amc/cmd"
	"github.com/makibytes/amc/log"
	"github.com/spf13/cobra"
)

// GetRootCommand returns the Artemis root command
func GetRootCommand() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "amc",
		Short: "Apache Artemis Messaging Client",
		Long:  "Command-line interface for Apache Artemis messaging",
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			if verbose, _ := cmd.Flags().GetBool("verbose"); verbose {
				log.IsVerbose = true
			}
		},
	}

	// Connection flags
	var defaultServer = os.Getenv("AMC_SERVER")
	if defaultServer == "" {
		defaultServer = "amqp://localhost:5672"
	}
	var defaultUser = os.Getenv("AMC_USER")
	var defaultPassword = os.Getenv("AMC_PASSWORD")

	var connArgs artemis.ConnArguments

	rootCmd.PersistentFlags().StringVarP(&connArgs.Server, "server", "s", defaultServer, "Server URL")
	rootCmd.PersistentFlags().StringVarP(&connArgs.User, "user", "u", defaultUser, "Username for SASL PLAIN login")
	rootCmd.PersistentFlags().StringVarP(&connArgs.Password, "password", "p", defaultPassword, "Password for SASL PLAIN login")
	rootCmd.PersistentFlags().BoolP("verbose", "v", false, "Print verbose output")

	// Add commands - Artemis supports both queues and topics
	// Queue commands
	sendCmd := cmd.NewSendCommand(nil)
	sendCmd.RunE = func(c *cobra.Command, args []string) error {
		adapter, err := artemis.NewQueueAdapter(connArgs)
		if err != nil {
			return err
		}
		defer adapter.Close()
		return cmd.NewSendCommand(adapter).RunE(c, args)
	}
	rootCmd.AddCommand(sendCmd)

	receiveCmd := cmd.NewReceiveCommand(nil)
	receiveCmd.RunE = func(c *cobra.Command, args []string) error {
		adapter, err := artemis.NewQueueAdapter(connArgs)
		if err != nil {
			return err
		}
		defer adapter.Close()
		return cmd.NewReceiveCommand(adapter).RunE(c, args)
	}
	rootCmd.AddCommand(receiveCmd)

	peekCmd := cmd.NewPeekCommand(nil)
	peekCmd.RunE = func(c *cobra.Command, args []string) error {
		adapter, err := artemis.NewQueueAdapter(connArgs)
		if err != nil {
			return err
		}
		defer adapter.Close()
		return cmd.NewPeekCommand(adapter).RunE(c, args)
	}
	rootCmd.AddCommand(peekCmd)

	// Topic commands
	publishCmd := cmd.NewPublishCommand(nil)
	publishCmd.RunE = func(c *cobra.Command, args []string) error {
		adapter, err := artemis.NewTopicAdapter(connArgs)
		if err != nil {
			return err
		}
		defer adapter.Close()
		return cmd.NewPublishCommand(adapter).RunE(c, args)
	}
	rootCmd.AddCommand(publishCmd)

	subscribeCmd := cmd.NewSubscribeCommand(nil)
	subscribeCmd.RunE = func(c *cobra.Command, args []string) error {
		adapter, err := artemis.NewTopicAdapter(connArgs)
		if err != nil {
			return err
		}
		defer adapter.Close()
		return cmd.NewSubscribeCommand(adapter).RunE(c, args)
	}
	rootCmd.AddCommand(subscribeCmd)

	return rootCmd
}