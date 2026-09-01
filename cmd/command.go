package cmd

import (
	"github.com/makibytes/xmc/broker/backends"
	"github.com/makibytes/xmc/log"
	"github.com/spf13/cobra"
)

// QueueAdapterFactory creates a QueueBackend from the current command context.
// This allows lazy connection: the adapter is only created when the command runs.
type QueueAdapterFactory func() (backends.QueueBackend, error)

// TopicAdapterFactory creates a TopicBackend from the current command context.
type TopicAdapterFactory func() (backends.TopicBackend, error)

// wrapCommand creates a command using a nil backend for flag definitions, then
// overrides RunE to lazily create the real adapter at execution time.
func wrapCommand[T Closeable](newCmd func(T) *cobra.Command, factory func() (T, error)) *cobra.Command {
	var zero T
	cmd := newCmd(zero)
	cmd.RunE = func(c *cobra.Command, args []string) error {
		adapter, err := factory()
		if err != nil {
			return err
		}
		defer func() {
			if cerr := adapter.Close(); cerr != nil {
				log.Verbose("close: %s", cerr)
			}
		}()
		return newCmd(adapter).RunE(c, args)
	}
	return cmd
}

// WrapQueueCommand creates a command using a nil backend for flag definitions,
// then overrides RunE to lazily create the real adapter at execution time.
func WrapQueueCommand(newCmd func(backends.QueueBackend) *cobra.Command, factory QueueAdapterFactory) *cobra.Command {
	return wrapCommand(newCmd, factory)
}

// WrapTopicCommand creates a command using a nil backend for flag definitions,
// then overrides RunE to lazily create the real adapter at execution time.
func WrapTopicCommand(newCmd func(backends.TopicBackend) *cobra.Command, factory TopicAdapterFactory) *cobra.Command {
	return wrapCommand(newCmd, factory)
}
