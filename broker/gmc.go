//go:build google

package broker

import (
	"os"

	"github.com/makibytes/xmc/broker/backends"
	gcppkg "github.com/makibytes/xmc/broker/gcppubsub"
	"github.com/makibytes/xmc/cmd"
	"github.com/spf13/cobra"
)

func GetRootCommand() *cobra.Command {
	var connArgs gcppkg.ConnArguments

	return cmd.NewRootCommand(cmd.BrokerSpec{
		Use:   "gmc",
		Short: "Google Pub/Sub Messaging Client",
		Long:  "Command-line interface for Google Cloud Pub/Sub messaging",
		RegisterFlags: func(c *cobra.Command) {
			c.PersistentFlags().StringVarP(&connArgs.Project, "project", "s", os.Getenv("GMC_PROJECT"), "Google Cloud project ID")
			c.PersistentFlags().StringVar(&connArgs.Credentials, "credentials", os.Getenv("GMC_CREDENTIALS"), "Path to service account credentials JSON file")
			c.PersistentFlags().StringVar(&connArgs.Endpoint, "endpoint", os.Getenv("GMC_SERVER"), "Pub/Sub API endpoint (e.g. for emulator)")
		},
		Queue: func() (backends.QueueBackend, error) { return gcppkg.NewQueueAdapter(connArgs) },
		Topic: func() (backends.TopicBackend, error) { return gcppkg.NewTopicAdapter(connArgs) },
		Ping:  func() (cmd.Closeable, error) { return gcppkg.NewQueueAdapter(connArgs) },
		Manage: cmd.NewManageCommand(cmd.ManageSpec{
			ListQueues: func() ([]backends.QueueInfo, error) { return gcppkg.ListSubscriptions(connArgs) },
			ListTopics: func() ([]backends.TopicInfo, error) { return gcppkg.ListTopics(connArgs) },
		}),
	})
}
