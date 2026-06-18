//go:build azmc

package broker

import (
	"fmt"
	"os"

	"github.com/makibytes/xmc/broker/backends"
	azpkg "github.com/makibytes/xmc/broker/azuresb"
	"github.com/makibytes/xmc/cmd"
	"github.com/makibytes/xmc/log"
	"github.com/spf13/cobra"
)

func GetRootCommand() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "azmc",
		Short: "Azure Service Bus Messaging Client",
		Long:  "Command-line interface for Azure Service Bus messaging",
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			if verbose, _ := cmd.Flags().GetBool("verbose"); verbose {
				log.IsVerbose = true
			}
		},
	}

	defaultConnectionString := os.Getenv("AZMC_CONNECTION_STRING")
	defaultNamespace := os.Getenv("AZMC_NAMESPACE")

	var connArgs azpkg.ConnArguments

	rootCmd.PersistentFlags().StringVarP(&connArgs.ConnectionString, "connection-string", "s", defaultConnectionString, "Service Bus connection string")
	rootCmd.PersistentFlags().StringVar(&connArgs.Namespace, "namespace", defaultNamespace, "Service Bus namespace FQDN (uses Azure AD)")
	rootCmd.PersistentFlags().BoolP("verbose", "v", false, "Print verbose output")

	queueFactory := cmd.QueueAdapterFactory(func() (backends.QueueBackend, error) {
		return azpkg.NewQueueAdapter(connArgs)
	})
	rootCmd.AddCommand(cmd.WrapQueueCommand(cmd.NewSendCommand, queueFactory))
	rootCmd.AddCommand(cmd.WrapQueueCommand(cmd.NewReceiveCommand, queueFactory))
	rootCmd.AddCommand(cmd.WrapQueueCommand(cmd.NewPeekCommand, queueFactory))
	rootCmd.AddCommand(cmd.WrapQueueCommand(cmd.NewRequestCommand, queueFactory))
	rootCmd.AddCommand(cmd.WrapQueueCommand(cmd.NewReplyCommand, queueFactory))
	rootCmd.AddCommand(cmd.WrapQueueCommand(cmd.NewMoveCommand, queueFactory))
	rootCmd.AddCommand(cmd.WrapQueueCommand(cmd.NewForwardCommand, queueFactory))

	topicFactory := cmd.TopicAdapterFactory(func() (backends.TopicBackend, error) {
		return azpkg.NewTopicAdapter(connArgs)
	})
	rootCmd.AddCommand(cmd.WrapTopicCommand(cmd.NewPublishCommand, topicFactory))
	rootCmd.AddCommand(cmd.WrapTopicCommand(cmd.NewSubscribeCommand, topicFactory))

	rootCmd.AddCommand(newAzureManageCommand(&connArgs))

	rootCmd.AddCommand(cmd.NewPingCommand(func() (cmd.Closeable, error) {
		return azpkg.NewQueueAdapter(connArgs)
	}))

	rootCmd.AddCommand(cmd.NewVersionCommand())

	return rootCmd
}

func newAzureManageCommand(connArgs *azpkg.ConnArguments) *cobra.Command {
	mgmtCmd := &cobra.Command{
		Use:   "manage",
		Short: "Broker management operations",
	}

	mgmtCmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List queues and topics",
		RunE: func(c *cobra.Command, args []string) error {
			queues, err := azpkg.ListQueues(*connArgs)
			if err != nil {
				return err
			}
			for _, q := range queues {
				fmt.Printf("queue  %-40s  messages=%d\n", q.Name, q.MessageCount)
			}
			topics, err := azpkg.ListTopics(*connArgs)
			if err != nil {
				return err
			}
			for _, t := range topics {
				fmt.Printf("topic  %s\n", t.Name)
			}
			return nil
		},
	})

	mgmtCmd.AddCommand(&cobra.Command{
		Use:   "purge [queue]",
		Short: "Purge all messages from a queue (drains via ReceiveAndDelete)",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			count, err := azpkg.PurgeQueue(*connArgs, args[0])
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
			stats, err := azpkg.GetQueueStats(*connArgs, args[0])
			if err != nil {
				return err
			}
			fmt.Printf("Queue:           %s\n", stats.Name)
			fmt.Printf("Active messages: %d\n", stats.MessageCount)
			fmt.Printf("Total messages:  %d\n", stats.EnqueueCount)
			return nil
		},
	})

	return mgmtCmd
}
