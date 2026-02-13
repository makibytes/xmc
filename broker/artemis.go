//go:build artemis

package broker

import (
	"fmt"
	"os"

	"github.com/makibytes/amc/broker/artemis"
	"github.com/makibytes/amc/broker/backends"
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

	// TLS flags
	rootCmd.PersistentFlags().BoolVar(&connArgs.TLS.Enabled, "tls", false, "Enable TLS connection")
	rootCmd.PersistentFlags().StringVar(&connArgs.TLS.CACert, "ca-cert", "", "Path to CA certificate file")
	rootCmd.PersistentFlags().StringVar(&connArgs.TLS.ClientCert, "cert", "", "Path to client certificate file")
	rootCmd.PersistentFlags().StringVar(&connArgs.TLS.ClientKey, "key-file", "", "Path to client private key file")
	rootCmd.PersistentFlags().BoolVar(&connArgs.TLS.Insecure, "insecure", false, "Skip TLS certificate verification")

	// Queue commands
	queueFactory := cmd.QueueAdapterFactory(func() (backends.QueueBackend, error) {
		return artemis.NewQueueAdapter(connArgs)
	})
	rootCmd.AddCommand(cmd.WrapQueueCommand(cmd.NewSendCommand, queueFactory))
	rootCmd.AddCommand(cmd.WrapQueueCommand(cmd.NewReceiveCommand, queueFactory))
	rootCmd.AddCommand(cmd.WrapQueueCommand(cmd.NewPeekCommand, queueFactory))
	rootCmd.AddCommand(cmd.WrapQueueCommand(cmd.NewRequestCommand, queueFactory))

	// Topic commands
	topicFactory := cmd.TopicAdapterFactory(func() (backends.TopicBackend, error) {
		return artemis.NewTopicAdapter(connArgs)
	})
	rootCmd.AddCommand(cmd.WrapTopicCommand(cmd.NewPublishCommand, topicFactory))
	rootCmd.AddCommand(cmd.WrapTopicCommand(cmd.NewSubscribeCommand, topicFactory))

	// Management commands
	rootCmd.AddCommand(newManageCommand(connArgs))

	return rootCmd
}

func newManageCommand(connArgs artemis.ConnArguments) *cobra.Command {
	mgmtCmd := &cobra.Command{
		Use:   "manage",
		Short: "Broker management operations (list, purge, stats)",
	}

	mgmtCmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List queues and topics",
		RunE: func(c *cobra.Command, args []string) error {
			mgmtArgs := artemis.ManagementArgs{
				Server: connArgs.Server, User: connArgs.User, Password: connArgs.Password,
			}
			queues, err := artemis.ListQueues(mgmtArgs)
			if err != nil {
				return err
			}
			for _, q := range queues {
				fmt.Printf("%-40s  type=%-10s  messages=%d  consumers=%d\n",
					q.Name, q.RoutingType, q.MessageCount, q.ConsumerCount)
			}
			return nil
		},
	})

	mgmtCmd.AddCommand(&cobra.Command{
		Use:   "purge <queue>",
		Short: "Remove all messages from a queue",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			mgmtArgs := artemis.ManagementArgs{
				Server: connArgs.Server, User: connArgs.User, Password: connArgs.Password,
			}
			count, err := artemis.PurgeQueue(mgmtArgs, args[0])
			if err != nil {
				return err
			}
			fmt.Printf("Purged %d messages from %s\n", count, args[0])
			return nil
		},
	})

	mgmtCmd.AddCommand(&cobra.Command{
		Use:   "stats <queue>",
		Short: "Show queue statistics",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			mgmtArgs := artemis.ManagementArgs{
				Server: connArgs.Server, User: connArgs.User, Password: connArgs.Password,
			}
			stats, err := artemis.GetQueueStats(mgmtArgs, args[0])
			if err != nil {
				return err
			}
			fmt.Printf("Queue:     %s\n", stats.Name)
			fmt.Printf("Messages:  %d\n", stats.MessageCount)
			fmt.Printf("Consumers: %d\n", stats.ConsumerCount)
			fmt.Printf("Enqueued:  %d\n", stats.EnqueueCount)
			fmt.Printf("Dequeued:  %d\n", stats.DequeueCount)
			return nil
		},
	})

	return mgmtCmd
}
