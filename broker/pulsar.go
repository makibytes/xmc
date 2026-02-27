//go:build pulsar

package broker

import (
	"fmt"
	"os"

	"github.com/makibytes/xmc/broker/backends"
	pulsarpkg "github.com/makibytes/xmc/broker/pulsar"
	"github.com/makibytes/xmc/cmd"
	"github.com/makibytes/xmc/log"
	"github.com/spf13/cobra"
)

// GetRootCommand returns the Pulsar root command.
func GetRootCommand() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "pmc",
		Short: "Pulsar Messaging Client",
		Long:  "Command-line interface for Apache Pulsar messaging",
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			if verbose, _ := cmd.Flags().GetBool("verbose"); verbose {
				log.IsVerbose = true
			}
		},
	}

	defaultServer := os.Getenv("PMC_SERVER")
	if defaultServer == "" {
		defaultServer = "pulsar://localhost:6650"
	}
	defaultUser := os.Getenv("PMC_USER")
	defaultPassword := os.Getenv("PMC_PASSWORD")

	var connArgs pulsarpkg.ConnArguments

	rootCmd.PersistentFlags().StringVarP(&connArgs.Server, "server", "s", defaultServer, "Server URL")
	rootCmd.PersistentFlags().StringVarP(&connArgs.User, "user", "u", defaultUser, "Username for authentication")
	rootCmd.PersistentFlags().StringVarP(&connArgs.Password, "password", "p", defaultPassword, "Password for authentication")
	rootCmd.PersistentFlags().BoolP("verbose", "v", false, "Print verbose output")

	// TLS flags
	rootCmd.PersistentFlags().BoolVar(&connArgs.TLS.Enabled, "tls", false, "Enable TLS connection")
	rootCmd.PersistentFlags().StringVar(&connArgs.TLS.CACert, "ca-cert", "", "Path to CA certificate file")
	rootCmd.PersistentFlags().StringVar(&connArgs.TLS.ClientCert, "cert", "", "Path to client certificate file")
	rootCmd.PersistentFlags().StringVar(&connArgs.TLS.ClientKey, "key-file", "", "Path to client private key file")
	rootCmd.PersistentFlags().BoolVar(&connArgs.TLS.Insecure, "insecure", false, "Skip TLS certificate verification")

	// Queue commands
	queueFactory := cmd.QueueAdapterFactory(func() (backends.QueueBackend, error) {
		return pulsarpkg.NewQueueAdapter(connArgs)
	})
	rootCmd.AddCommand(cmd.WrapQueueCommand(cmd.NewSendCommand, queueFactory))
	rootCmd.AddCommand(cmd.WrapQueueCommand(cmd.NewReceiveCommand, queueFactory))
	rootCmd.AddCommand(cmd.WrapQueueCommand(cmd.NewPeekCommand, queueFactory))
	rootCmd.AddCommand(cmd.WrapQueueCommand(cmd.NewRequestCommand, queueFactory))

	// Topic commands
	topicFactory := cmd.TopicAdapterFactory(func() (backends.TopicBackend, error) {
		return pulsarpkg.NewTopicAdapter(connArgs)
	})
	rootCmd.AddCommand(cmd.WrapTopicCommand(cmd.NewPublishCommand, topicFactory))
	rootCmd.AddCommand(cmd.WrapTopicCommand(cmd.NewSubscribeCommand, topicFactory))

	// Management commands
	rootCmd.AddCommand(newPulsarManageCommand(&connArgs))

	// Version command
	rootCmd.AddCommand(cmd.NewVersionCommand())

	return rootCmd
}

func newPulsarManageCommand(connArgs *pulsarpkg.ConnArguments) *cobra.Command {
	var adminPort int

	mgmtCmd := &cobra.Command{
		Use:   "manage",
		Short: "Broker management operations",
	}

	mgmtCmd.PersistentFlags().IntVar(&adminPort, "admin-port", 8080, "Pulsar admin REST API port")

	mgmtCmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List topics in public/default namespace",
		RunE: func(c *cobra.Command, args []string) error {
			topics, err := pulsarpkg.ListTopics(*connArgs, adminPort)
			if err != nil {
				return err
			}
			for _, t := range topics {
				fmt.Println(t.Name)
			}
			return nil
		},
	})

	return mgmtCmd
}
