//go:build redmc

package broker

import (
	"fmt"
	"os"

	"github.com/makibytes/xmc/broker/backends"
	redispkg "github.com/makibytes/xmc/broker/redis"
	"github.com/makibytes/xmc/cmd"
	"github.com/makibytes/xmc/log"
	"github.com/spf13/cobra"
)

func GetRootCommand() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "redmc",
		Short: "Redis Messaging Client",
		Long:  "Command-line interface for Redis messaging (Streams)",
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			if verbose, _ := cmd.Flags().GetBool("verbose"); verbose {
				log.IsVerbose = true
			}
		},
	}

	defaultServer := os.Getenv("REDMC_SERVER")
	if defaultServer == "" {
		defaultServer = "redis://localhost:6379"
	}
	defaultUser := os.Getenv("REDMC_USER")
	defaultPassword := os.Getenv("REDMC_PASSWORD")

	var connArgs redispkg.ConnArguments

	rootCmd.PersistentFlags().StringVarP(&connArgs.Server, "server", "s", defaultServer, "Server URL")
	rootCmd.PersistentFlags().StringVarP(&connArgs.User, "user", "u", defaultUser, "Username for authentication")
	rootCmd.PersistentFlags().StringVarP(&connArgs.Password, "password", "p", defaultPassword, "Password for authentication")
	rootCmd.PersistentFlags().BoolP("verbose", "v", false, "Print verbose output")

	rootCmd.PersistentFlags().BoolVar(&connArgs.TLS.Enabled, "tls", false, "Enable TLS connection")
	rootCmd.PersistentFlags().StringVar(&connArgs.TLS.CACert, "ca-cert", "", "Path to CA certificate file")
	rootCmd.PersistentFlags().StringVar(&connArgs.TLS.ClientCert, "cert", "", "Path to client certificate file")
	rootCmd.PersistentFlags().StringVar(&connArgs.TLS.ClientKey, "key-file", "", "Path to client private key file")
	rootCmd.PersistentFlags().BoolVar(&connArgs.TLS.Insecure, "insecure", false, "Skip TLS certificate verification")

	queueFactory := cmd.QueueAdapterFactory(func() (backends.QueueBackend, error) {
		return redispkg.NewQueueAdapter(connArgs)
	})
	rootCmd.AddCommand(cmd.WrapQueueCommand(cmd.NewSendCommand, queueFactory))
	rootCmd.AddCommand(cmd.WrapQueueCommand(cmd.NewReceiveCommand, queueFactory))
	rootCmd.AddCommand(cmd.WrapQueueCommand(cmd.NewPeekCommand, queueFactory))
	rootCmd.AddCommand(cmd.WrapQueueCommand(cmd.NewRequestCommand, queueFactory))
	rootCmd.AddCommand(cmd.WrapQueueCommand(cmd.NewReplyCommand, queueFactory))
	rootCmd.AddCommand(cmd.WrapQueueCommand(cmd.NewMoveCommand, queueFactory))
	rootCmd.AddCommand(cmd.WrapQueueCommand(cmd.NewForwardCommand, queueFactory))

	topicFactory := cmd.TopicAdapterFactory(func() (backends.TopicBackend, error) {
		return redispkg.NewTopicAdapter(connArgs)
	})
	rootCmd.AddCommand(cmd.WrapTopicCommand(cmd.NewPublishCommand, topicFactory))
	rootCmd.AddCommand(cmd.WrapTopicCommand(cmd.NewSubscribeCommand, topicFactory))

	rootCmd.AddCommand(newRedisManageCommand(&connArgs))

	rootCmd.AddCommand(cmd.NewPingCommand(func() (cmd.Closeable, error) {
		return redispkg.NewQueueAdapter(connArgs)
	}))

	rootCmd.AddCommand(cmd.NewVersionCommand())

	return rootCmd
}

func newRedisManageCommand(connArgs *redispkg.ConnArguments) *cobra.Command {
	mgmtCmd := &cobra.Command{
		Use:   "manage",
		Short: "Broker management operations",
	}

	mgmtCmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List queues and topics",
		RunE: func(c *cobra.Command, args []string) error {
			queues, err := redispkg.ListQueues(*connArgs)
			if err != nil {
				return err
			}
			for _, q := range queues {
				fmt.Printf("queue  %-40s  messages=%d\n", q.Name, q.MessageCount)
			}
			topics, err := redispkg.ListTopics(*connArgs)
			if err != nil {
				return err
			}
			for _, t := range topics {
				fmt.Printf("topic  %-40s\n", t.Name)
			}
			return nil
		},
	})

	mgmtCmd.AddCommand(&cobra.Command{
		Use:   "purge [queue]",
		Short: "Purge all messages from a queue",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			count, err := redispkg.PurgeQueue(*connArgs, args[0])
			if err != nil {
				return err
			}
			fmt.Printf("Purged %d messages from queue %s\n", count, args[0])
			return nil
		},
	})

	mgmtCmd.AddCommand(&cobra.Command{
		Use:   "stats [queue]",
		Short: "Show queue statistics",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			stats, err := redispkg.GetQueueStats(*connArgs, args[0])
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
