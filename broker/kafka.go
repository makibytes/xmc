//go:build kafka

package broker

import (
	"fmt"
	"os"

	"github.com/makibytes/xmc/broker/backends"
	"github.com/makibytes/xmc/broker/kafka"
	"github.com/makibytes/xmc/cmd"
	"github.com/makibytes/xmc/log"
	"github.com/spf13/cobra"
)

// GetRootCommand returns the Kafka root command
func GetRootCommand() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "kmc",
		Short: "Apache Kafka Messaging Client",
		Long:  "Command-line interface for Apache Kafka messaging",
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			if verbose, _ := cmd.Flags().GetBool("verbose"); verbose {
				log.IsVerbose = true
			}
		},
	}

	// Connection flags
	var defaultServer = os.Getenv("KMC_SERVER")
	if defaultServer == "" {
		defaultServer = "kafka://localhost:9092"
	}
	var defaultUser = os.Getenv("KMC_USER")
	var defaultPassword = os.Getenv("KMC_PASSWORD")

	var connArgs kafka.ConnArguments

	rootCmd.PersistentFlags().StringVarP(&connArgs.Server, "server", "s", defaultServer, "Server URL (kafka://broker1:9092 or kafka://broker1:9092,broker2:9092)")
	rootCmd.PersistentFlags().StringVarP(&connArgs.User, "user", "u", defaultUser, "Username for SASL authentication")
	rootCmd.PersistentFlags().StringVarP(&connArgs.Password, "password", "p", defaultPassword, "Password for SASL authentication")
	rootCmd.PersistentFlags().BoolP("verbose", "v", false, "Print verbose output")

	// TLS flags
	rootCmd.PersistentFlags().BoolVar(&connArgs.TLS.Enabled, "tls", false, "Enable TLS connection")
	rootCmd.PersistentFlags().StringVar(&connArgs.TLS.CACert, "ca-cert", "", "Path to CA certificate file")
	rootCmd.PersistentFlags().StringVar(&connArgs.TLS.ClientCert, "cert", "", "Path to client certificate file")
	rootCmd.PersistentFlags().StringVar(&connArgs.TLS.ClientKey, "key-file", "", "Path to client private key file")
	rootCmd.PersistentFlags().BoolVar(&connArgs.TLS.Insecure, "insecure", false, "Skip TLS certificate verification")

	// Topic commands (Kafka is topic-only)
	topicFactory := cmd.TopicAdapterFactory(func() (backends.TopicBackend, error) {
		return kafka.NewTopicAdapter(connArgs)
	})
	rootCmd.AddCommand(cmd.WrapTopicCommand(cmd.NewPublishCommand, topicFactory))
	rootCmd.AddCommand(cmd.WrapTopicCommand(cmd.NewSubscribeCommand, topicFactory))

	// Management commands
	mgmtCmd := &cobra.Command{
		Use:   "manage",
		Short: "Broker management operations (list)",
	}
	mgmtCmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List topics",
		RunE: func(c *cobra.Command, args []string) error {
			topics, err := kafka.ListTopics(connArgs)
			if err != nil {
				return err
			}
			for _, t := range topics {
				fmt.Printf("%-40s  partitions=%d\n", t.Name, t.PartitionCount)
			}
			return nil
		},
	})
	rootCmd.AddCommand(mgmtCmd)

	// Version command
	rootCmd.AddCommand(cmd.NewVersionCommand())

	return rootCmd
}
