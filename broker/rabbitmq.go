//go:build rabbitmq

package broker

import (
	"os"

	"github.com/makibytes/amc/broker/rabbitmq"
	"github.com/makibytes/amc/cmd"
	"github.com/makibytes/amc/log"
	"github.com/spf13/cobra"
)

// GetRootCommand returns the RabbitMQ root command
func GetRootCommand() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "amc",
		Short: "RabbitMQ Messaging Client",
		Long:  "Command-line interface for RabbitMQ messaging (AMQP 1.0)",
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
	var defaultExchange = os.Getenv("AMC_EXCHANGE")
	if defaultExchange == "" {
		defaultExchange = "amq.topic"
	}

	var connArgs rabbitmq.ConnArguments
	var exchange string

	rootCmd.PersistentFlags().StringVarP(&connArgs.Server, "server", "s", defaultServer, "Server URL")
	rootCmd.PersistentFlags().StringVarP(&connArgs.User, "user", "u", defaultUser, "Username for SASL PLAIN login")
	rootCmd.PersistentFlags().StringVarP(&connArgs.Password, "password", "p", defaultPassword, "Password for SASL PLAIN login")
	rootCmd.PersistentFlags().StringVarP(&exchange, "exchange", "e", defaultExchange, "Exchange name for topic operations")
	rootCmd.PersistentFlags().BoolP("verbose", "v", false, "Print verbose output")

	// Add commands - RabbitMQ supports both queues and topics
	// Queue commands (direct routing)
	sendCmd := cmd.NewSendCommand(nil)
	sendCmd.RunE = func(c *cobra.Command, args []string) error {
		adapter, err := rabbitmq.NewQueueAdapter(connArgs)
		if err != nil {
			return err
		}
		defer adapter.Close()
		return cmd.NewSendCommand(adapter).RunE(c, args)
	}
	rootCmd.AddCommand(sendCmd)

	receiveCmd := cmd.NewReceiveCommand(nil)
	receiveCmd.RunE = func(c *cobra.Command, args []string) error {
		adapter, err := rabbitmq.NewQueueAdapter(connArgs)
		if err != nil {
			return err
		}
		defer adapter.Close()
		return cmd.NewReceiveCommand(adapter).RunE(c, args)
	}
	rootCmd.AddCommand(receiveCmd)

	peekCmd := cmd.NewPeekCommand(nil)
	peekCmd.RunE = func(c *cobra.Command, args []string) error {
		adapter, err := rabbitmq.NewQueueAdapter(connArgs)
		if err != nil {
			return err
		}
		defer adapter.Close()
		return cmd.NewPeekCommand(adapter).RunE(c, args)
	}
	rootCmd.AddCommand(peekCmd)

	// Topic commands (exchange-based routing)
	publishCmd := cmd.NewPublishCommand(nil)
	publishCmd.RunE = func(c *cobra.Command, args []string) error {
		adapter, err := rabbitmq.NewTopicAdapter(connArgs, exchange)
		if err != nil {
			return err
		}
		defer adapter.Close()
		return cmd.NewPublishCommand(adapter).RunE(c, args)
	}
	rootCmd.AddCommand(publishCmd)

	subscribeCmd := cmd.NewSubscribeCommand(nil)
	subscribeCmd.RunE = func(c *cobra.Command, args []string) error {
		adapter, err := rabbitmq.NewTopicAdapter(connArgs, exchange)
		if err != nil {
			return err
		}
		defer adapter.Close()
		return cmd.NewSubscribeCommand(adapter).RunE(c, args)
	}
	rootCmd.AddCommand(subscribeCmd)

	return rootCmd
}
