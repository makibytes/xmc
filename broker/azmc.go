//go:build azure

package broker

import (
	"os"

	"github.com/makibytes/xmc/broker/backends"
	azpkg "github.com/makibytes/xmc/broker/azuresb"
	"github.com/makibytes/xmc/cmd"
	"github.com/spf13/cobra"
)

func GetRootCommand() *cobra.Command {
	var connArgs azpkg.ConnArguments

	return cmd.NewRootCommand(cmd.BrokerSpec{
		Use:   "azmc",
		Short: "Azure Service Bus Messaging Client",
		Long:  "Command-line interface for Azure Service Bus messaging",
		RegisterFlags: func(c *cobra.Command) {
			c.PersistentFlags().StringVarP(&connArgs.ConnectionString, "connection-string", "s", os.Getenv("AZMC_CONNECTION_STRING"), "Service Bus connection string")
			c.PersistentFlags().StringVar(&connArgs.Namespace, "namespace", os.Getenv("AZMC_NAMESPACE"), "Service Bus namespace FQDN (uses Azure AD)")
		},
		Queue: func() (backends.QueueBackend, error) { return azpkg.NewQueueAdapter(connArgs) },
		Topic: func() (backends.TopicBackend, error) { return azpkg.NewTopicAdapter(connArgs) },
		Ping:  func() (cmd.Closeable, error) { return azpkg.NewQueueAdapter(connArgs) },
		Manage: cmd.NewManageCommand(cmd.ManageSpec{
			ListQueues: func() ([]backends.QueueInfo, error) { return azpkg.ListQueues(connArgs) },
			ListTopics: func() ([]backends.TopicInfo, error) { return azpkg.ListTopics(connArgs) },
			Purge:      func(queue string) (int64, error) { return azpkg.PurgeQueue(connArgs, queue) },
			Stats:      func(queue string) (*backends.QueueStats, error) { return azpkg.GetQueueStats(connArgs, queue) },
		}),
	})
}
