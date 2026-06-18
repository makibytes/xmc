package cmd

import (
	"github.com/makibytes/xmc/broker/backends"
	"github.com/makibytes/xmc/log"
	"github.com/spf13/cobra"
)

// BrokerSpec fully describes a broker flavour so that NewRootCommand can build
// the entire CLI command tree. Each entry file only needs to fill in this
// struct with its broker-specific wiring.
type BrokerSpec struct {
	// Use, Short, Long are passed to the root cobra.Command.
	Use, Short, Long string

	// RegisterFlags adds broker-specific persistent flags (connection,
	// TLS, etc.) to the root command. Called once during construction.
	RegisterFlags func(cmd *cobra.Command)

	// Queue and Topic are the lazy adapter factories. Nil means the broker
	// does not support that messaging model (e.g. Kafka is topic-only,
	// IBM MQ is queue-only).
	Queue QueueAdapterFactory
	Topic TopicAdapterFactory

	// Ping creates a short-lived connection to verify broker reachability.
	Ping Connector

	// Manage is an optional pre-built "manage" command (see NewManageCommand).
	Manage *cobra.Command

	// Extra holds broker-specific commands that don't fit the standard set
	// (e.g. Kafka's forward-topic, Artemis's MCP server).
	Extra []*cobra.Command
}

// NewRootCommand builds the full CLI command tree from a BrokerSpec. It
// replaces the ~100-line GetRootCommand function previously duplicated in
// each broker entry file.
func NewRootCommand(spec BrokerSpec) *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   spec.Use,
		Short: spec.Short,
		Long:  spec.Long,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			if verbose, _ := cmd.Flags().GetBool("verbose"); verbose {
				log.IsVerbose = true
			}
		},
	}

	rootCmd.PersistentFlags().BoolP("verbose", "v", false, "Print verbose output")

	if spec.RegisterFlags != nil {
		spec.RegisterFlags(rootCmd)
	}

	// Queue commands.
	if spec.Queue != nil {
		for _, newCmd := range []func(backends.QueueBackend) *cobra.Command{
			NewSendCommand,
			NewReceiveCommand,
			NewPeekCommand,
			NewRequestCommand,
			NewReplyCommand,
			NewMoveCommand,
			NewForwardCommand,
		} {
			rootCmd.AddCommand(WrapQueueCommand(newCmd, spec.Queue))
		}
	}

	// Topic commands.
	if spec.Topic != nil {
		for _, newCmd := range []func(backends.TopicBackend) *cobra.Command{
			NewPublishCommand,
			NewSubscribeCommand,
		} {
			rootCmd.AddCommand(WrapTopicCommand(newCmd, spec.Topic))
		}
	}

	// Management.
	if spec.Manage != nil {
		rootCmd.AddCommand(spec.Manage)
	}

	// Extra broker-specific commands.
	for _, extra := range spec.Extra {
		rootCmd.AddCommand(extra)
	}

	// Connectivity check.
	if spec.Ping != nil {
		rootCmd.AddCommand(NewPingCommand(spec.Ping))
	}

	rootCmd.AddCommand(NewVersionCommand())

	return rootCmd
}
