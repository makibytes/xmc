//go:build gmc

package broker

import (
	"fmt"
	"os"

	"github.com/makibytes/xmc/broker/backends"
	gcppkg "github.com/makibytes/xmc/broker/gcppubsub"
	"github.com/makibytes/xmc/cmd"
	"github.com/makibytes/xmc/log"
	"github.com/spf13/cobra"
)

func GetRootCommand() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "gmc",
		Short: "Google Pub/Sub Messaging Client",
		Long:  "Command-line interface for Google Cloud Pub/Sub messaging",
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			if verbose, _ := cmd.Flags().GetBool("verbose"); verbose {
				log.IsVerbose = true
			}
		},
	}

	defaultProject := os.Getenv("GMC_PROJECT")
	defaultCredentials := os.Getenv("GMC_CREDENTIALS")
	defaultEndpoint := os.Getenv("GMC_SERVER")

	var connArgs gcppkg.ConnArguments

	rootCmd.PersistentFlags().StringVarP(&connArgs.Project, "project", "s", defaultProject, "Google Cloud project ID")
	rootCmd.PersistentFlags().StringVar(&connArgs.Credentials, "credentials", defaultCredentials, "Path to service account credentials JSON file")
	rootCmd.PersistentFlags().StringVar(&connArgs.Endpoint, "endpoint", defaultEndpoint, "Pub/Sub API endpoint (e.g. for emulator)")
	rootCmd.PersistentFlags().BoolP("verbose", "v", false, "Print verbose output")

	queueFactory := cmd.QueueAdapterFactory(func() (backends.QueueBackend, error) {
		return gcppkg.NewQueueAdapter(connArgs)
	})
	rootCmd.AddCommand(cmd.WrapQueueCommand(cmd.NewSendCommand, queueFactory))
	rootCmd.AddCommand(cmd.WrapQueueCommand(cmd.NewReceiveCommand, queueFactory))
	rootCmd.AddCommand(cmd.WrapQueueCommand(cmd.NewPeekCommand, queueFactory))
	rootCmd.AddCommand(cmd.WrapQueueCommand(cmd.NewRequestCommand, queueFactory))
	rootCmd.AddCommand(cmd.WrapQueueCommand(cmd.NewReplyCommand, queueFactory))
	rootCmd.AddCommand(cmd.WrapQueueCommand(cmd.NewMoveCommand, queueFactory))
	rootCmd.AddCommand(cmd.WrapQueueCommand(cmd.NewForwardCommand, queueFactory))

	topicFactory := cmd.TopicAdapterFactory(func() (backends.TopicBackend, error) {
		return gcppkg.NewTopicAdapter(connArgs)
	})
	rootCmd.AddCommand(cmd.WrapTopicCommand(cmd.NewPublishCommand, topicFactory))
	rootCmd.AddCommand(cmd.WrapTopicCommand(cmd.NewSubscribeCommand, topicFactory))

	rootCmd.AddCommand(newGCPManageCommand(&connArgs))

	rootCmd.AddCommand(cmd.NewPingCommand(func() (cmd.Closeable, error) {
		return gcppkg.NewQueueAdapter(connArgs)
	}))

	rootCmd.AddCommand(cmd.NewVersionCommand())

	return rootCmd
}

func newGCPManageCommand(connArgs *gcppkg.ConnArguments) *cobra.Command {
	mgmtCmd := &cobra.Command{
		Use:   "manage",
		Short: "Broker management operations",
	}

	mgmtCmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List topics and subscriptions",
		RunE: func(c *cobra.Command, args []string) error {
			topics, err := gcppkg.ListTopics(*connArgs)
			if err != nil {
				return err
			}
			for _, t := range topics {
				fmt.Printf("topic  %s\n", t.Name)
			}
			subs, err := gcppkg.ListSubscriptions(*connArgs)
			if err != nil {
				return err
			}
			for _, s := range subs {
				fmt.Printf("subscription  %s\n", s.Name)
			}
			return nil
		},
	})

	return mgmtCmd
}
