package cmd

import (
	"github.com/makibytes/amc/broker/backends"
	"github.com/spf13/cobra"
)

// QueueAdapterFactory creates a QueueBackend from the current command context.
// This allows lazy connection: the adapter is only created when the command runs.
type QueueAdapterFactory func() (backends.QueueBackend, error)

// TopicAdapterFactory creates a TopicBackend from the current command context.
type TopicAdapterFactory func() (backends.TopicBackend, error)

// WrapQueueCommand creates a command using a nil backend for flag definitions,
// then overrides RunE to lazily create the real adapter at execution time.
func WrapQueueCommand(newCmd func(backends.QueueBackend) *cobra.Command, factory QueueAdapterFactory) *cobra.Command {
	cmd := newCmd(nil)
	cmd.RunE = func(c *cobra.Command, args []string) error {
		adapter, err := factory()
		if err != nil {
			return err
		}
		defer adapter.Close()
		return newCmd(adapter).RunE(c, args)
	}
	return cmd
}

// WrapTopicCommand creates a command using a nil backend for flag definitions,
// then overrides RunE to lazily create the real adapter at execution time.
func WrapTopicCommand(newCmd func(backends.TopicBackend) *cobra.Command, factory TopicAdapterFactory) *cobra.Command {
	cmd := newCmd(nil)
	cmd.RunE = func(c *cobra.Command, args []string) error {
		adapter, err := factory()
		if err != nil {
			return err
		}
		defer adapter.Close()
		return newCmd(adapter).RunE(c, args)
	}
	return cmd
}
