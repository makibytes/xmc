//go:build nats

package broker

import (
	"fmt"
	"os"

	"github.com/makibytes/xmc/broker/backends"
	natspkg "github.com/makibytes/xmc/broker/nats"
	"github.com/makibytes/xmc/cmd"
	"github.com/makibytes/xmc/log"
	"github.com/spf13/cobra"
)

// GetRootCommand returns the NATS root command.
func GetRootCommand() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "nmc",
		Short: "NATS Messaging Client",
		Long:  "Command-line interface for NATS messaging",
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			if verbose, _ := cmd.Flags().GetBool("verbose"); verbose {
				log.IsVerbose = true
			}
		},
	}

	defaultServer := os.Getenv("NMC_SERVER")
	if defaultServer == "" {
		defaultServer = "nats://localhost:4222"
	}
	defaultUser := os.Getenv("NMC_USER")
	defaultPassword := os.Getenv("NMC_PASSWORD")

	var connArgs natspkg.ConnArguments

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

	// Queue commands (JetStream)
	queueFactory := cmd.QueueAdapterFactory(func() (backends.QueueBackend, error) {
		return natspkg.NewQueueAdapter(connArgs)
	})
	rootCmd.AddCommand(cmd.WrapQueueCommand(cmd.NewSendCommand, queueFactory))
	rootCmd.AddCommand(cmd.WrapQueueCommand(cmd.NewReceiveCommand, queueFactory))
	rootCmd.AddCommand(cmd.WrapQueueCommand(cmd.NewPeekCommand, queueFactory))
	rootCmd.AddCommand(cmd.WrapQueueCommand(cmd.NewRequestCommand, queueFactory))

	// Topic commands (core NATS pub/sub)
	topicFactory := cmd.TopicAdapterFactory(func() (backends.TopicBackend, error) {
		return natspkg.NewTopicAdapter(connArgs)
	})
	rootCmd.AddCommand(cmd.WrapTopicCommand(cmd.NewPublishCommand, topicFactory))
	rootCmd.AddCommand(cmd.WrapTopicCommand(cmd.NewSubscribeCommand, topicFactory))

	// Management commands
	rootCmd.AddCommand(newNATSManageCommand(connArgs))

	// Version command
	rootCmd.AddCommand(cmd.NewVersionCommand())

	return rootCmd
}

func newNATSManageCommand(connArgs natspkg.ConnArguments) *cobra.Command {
	mgmtCmd := &cobra.Command{
		Use:   "manage",
		Short: "Broker management operations",
	}

	mgmtCmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List JetStream streams (XMC queues)",
		RunE: func(c *cobra.Command, args []string) error {
			streams, err := natspkg.ListStreams(connArgs)
			if err != nil {
				return err
			}
			for _, s := range streams {
				fmt.Printf("%-40s  messages=%d\n", s.Name, s.MessageCount)
			}
			return nil
		},
	})

	return mgmtCmd
}
