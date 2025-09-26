//go:build kafka

package broker

import (
	"os"

	"github.com/makibytes/amc/broker/kafka"
	"github.com/makibytes/amc/cmd"
	"github.com/makibytes/amc/log"
	"github.com/spf13/cobra"
)

// GetRootCommand returns the Kafka root command
func GetRootCommand() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "amc",
		Short: "Apache Kafka Messaging Client",
		Long:  "Command-line interface for Apache Kafka messaging",
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			if verbose, _ := cmd.Flags().GetBool("verbose"); verbose {
				log.IsVerbose = true
			}
		},
	}

	// Connection flags
	var defaultServer = os.Getenv("AMC_SERVER")
	if defaultServer == "" {
		defaultServer = "kafka://localhost:9092"
	}
	var defaultUser = os.Getenv("AMC_USER")
	var defaultPassword = os.Getenv("AMC_PASSWORD")

	var connArgs kafka.ConnArguments

	rootCmd.PersistentFlags().StringVarP(&connArgs.Server, "server", "s", defaultServer, "Server URL (kafka://broker1:9092 or kafka://broker1:9092,broker2:9092)")
	rootCmd.PersistentFlags().StringVarP(&connArgs.User, "user", "u", defaultUser, "Username for SASL authentication")
	rootCmd.PersistentFlags().StringVarP(&connArgs.Password, "password", "p", defaultPassword, "Password for SASL authentication")
	rootCmd.PersistentFlags().BoolP("verbose", "v", false, "Print verbose output")

	// Add commands - Kafka supports topics only
	publishCmd := cmd.NewPublishCommand(nil)
	publishCmd.RunE = func(c *cobra.Command, args []string) error {
		adapter, err := kafka.NewTopicAdapter(connArgs)
		if err != nil {
			return err
		}
		defer adapter.Close()
		return cmd.NewPublishCommand(adapter).RunE(c, args)
	}
	rootCmd.AddCommand(publishCmd)

	subscribeCmd := cmd.NewSubscribeCommand(nil)
	subscribeCmd.RunE = func(c *cobra.Command, args []string) error {
		adapter, err := kafka.NewTopicAdapter(connArgs)
		if err != nil {
			return err
		}
		defer adapter.Close()
		return cmd.NewSubscribeCommand(adapter).RunE(c, args)
	}
	rootCmd.AddCommand(subscribeCmd)

	return rootCmd
}