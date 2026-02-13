//go:build rabbitmq

package broker

import (
	"fmt"
	"os"

	"github.com/makibytes/xmc/broker/backends"
	"github.com/makibytes/xmc/broker/rabbitmq"
	"github.com/makibytes/xmc/cmd"
	"github.com/makibytes/xmc/log"
	"github.com/spf13/cobra"
)

// GetRootCommand returns the RabbitMQ root command
func GetRootCommand() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "rmc",
		Short: "RabbitMQ Messaging Client",
		Long:  "Command-line interface for RabbitMQ messaging (AMQP 1.0)",
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			if verbose, _ := cmd.Flags().GetBool("verbose"); verbose {
				log.IsVerbose = true
			}
		},
	}

	// Connection flags
	var defaultServer = os.Getenv("RMC_SERVER")
	if defaultServer == "" {
		defaultServer = "amqp://localhost:5672"
	}
	var defaultUser = os.Getenv("RMC_USER")
	var defaultPassword = os.Getenv("RMC_PASSWORD")
	var defaultExchange = os.Getenv("RMC_EXCHANGE")
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

	// TLS flags
	rootCmd.PersistentFlags().BoolVar(&connArgs.TLS.Enabled, "tls", false, "Enable TLS connection")
	rootCmd.PersistentFlags().StringVar(&connArgs.TLS.CACert, "ca-cert", "", "Path to CA certificate file")
	rootCmd.PersistentFlags().StringVar(&connArgs.TLS.ClientCert, "cert", "", "Path to client certificate file")
	rootCmd.PersistentFlags().StringVar(&connArgs.TLS.ClientKey, "key-file", "", "Path to client private key file")
	rootCmd.PersistentFlags().BoolVar(&connArgs.TLS.Insecure, "insecure", false, "Skip TLS certificate verification")

	// Queue commands (direct routing)
	queueFactory := cmd.QueueAdapterFactory(func() (backends.QueueBackend, error) {
		return rabbitmq.NewQueueAdapter(connArgs)
	})
	rootCmd.AddCommand(cmd.WrapQueueCommand(cmd.NewSendCommand, queueFactory))
	rootCmd.AddCommand(cmd.WrapQueueCommand(cmd.NewReceiveCommand, queueFactory))
	rootCmd.AddCommand(cmd.WrapQueueCommand(cmd.NewPeekCommand, queueFactory))
	rootCmd.AddCommand(cmd.WrapQueueCommand(cmd.NewRequestCommand, queueFactory))

	// Topic commands (exchange-based routing)
	topicFactory := cmd.TopicAdapterFactory(func() (backends.TopicBackend, error) {
		return rabbitmq.NewTopicAdapter(connArgs, exchange)
	})
	rootCmd.AddCommand(cmd.WrapTopicCommand(cmd.NewPublishCommand, topicFactory))
	rootCmd.AddCommand(cmd.WrapTopicCommand(cmd.NewSubscribeCommand, topicFactory))

	// Management commands
	rootCmd.AddCommand(newRabbitMQManageCommand(connArgs))

	// Version command
	rootCmd.AddCommand(cmd.NewVersionCommand())

	return rootCmd
}

func newRabbitMQManageCommand(connArgs rabbitmq.ConnArguments) *cobra.Command {
	mgmtCmd := &cobra.Command{
		Use:   "manage",
		Short: "Broker management operations (list, purge, stats)",
	}

	mgmtCmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List queues",
		RunE: func(c *cobra.Command, args []string) error {
			mgmtArgs := rabbitmq.ManagementArgs{
				Server: connArgs.Server, User: connArgs.User, Password: connArgs.Password,
			}
			queues, err := rabbitmq.ListQueues(mgmtArgs)
			if err != nil {
				return err
			}
			for _, q := range queues {
				fmt.Printf("%-40s  messages=%d  consumers=%d\n",
					q.Name, q.MessageCount, q.ConsumerCount)
			}
			return nil
		},
	})

	mgmtCmd.AddCommand(&cobra.Command{
		Use:   "purge <queue>",
		Short: "Remove all messages from a queue",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			mgmtArgs := rabbitmq.ManagementArgs{
				Server: connArgs.Server, User: connArgs.User, Password: connArgs.Password,
			}
			err := rabbitmq.PurgeQueue(mgmtArgs, args[0])
			if err != nil {
				return err
			}
			fmt.Printf("Purged queue %s\n", args[0])
			return nil
		},
	})

	mgmtCmd.AddCommand(&cobra.Command{
		Use:   "stats <queue>",
		Short: "Show queue statistics",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			mgmtArgs := rabbitmq.ManagementArgs{
				Server: connArgs.Server, User: connArgs.User, Password: connArgs.Password,
			}
			stats, err := rabbitmq.GetQueueStats(mgmtArgs, args[0])
			if err != nil {
				return err
			}
			fmt.Printf("Queue:     %s\n", stats.Name)
			fmt.Printf("Messages:  %d\n", stats.MessageCount)
			fmt.Printf("Consumers: %d\n", stats.ConsumerCount)
			return nil
		},
	})

	return mgmtCmd
}
