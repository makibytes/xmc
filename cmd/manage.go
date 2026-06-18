package cmd

import (
	"fmt"

	"github.com/makibytes/xmc/broker/backends"
	"github.com/spf13/cobra"
)

// ManageAction is one create/delete subcommand: optional flags + a run closure
// that closes over those flag-bound vars (same lazy-capture idiom as the rest
// of the spec).
type ManageAction struct {
	SetupFlags func(c *cobra.Command) // optional
	Run        func(name string) error
}

// BindAction is a binding subcommand taking <queue> <exchange> (+ optional
// flags like --routing-key bound in SetupFlags).
type BindAction struct {
	SetupFlags func(c *cobra.Command) // optional
	Run        func(queue, exchange string) error
}

// ManageSpec describes the management capabilities a broker exposes. Each
// closure is optional — nil means the broker does not support that operation,
// and the corresponding subcommand is omitted.
type ManageSpec struct {
	// ListQueues returns all queues known to the broker.
	ListQueues func() ([]backends.QueueInfo, error)
	// ListTopics returns all topics known to the broker.
	ListTopics func() ([]backends.TopicInfo, error)
	// Purge removes all messages from a queue and returns the count removed
	// (or 0 when the broker does not report a count).
	Purge func(queue string) (int64, error)
	// Stats returns detailed statistics for a single queue.
	Stats func(queue string) (*backends.QueueStats, error)
	// SetupFlags registers additional persistent flags on the manage command
	// (e.g. Pulsar's --admin-port).
	SetupFlags func(cmd *cobra.Command)

	// Resource lifecycle operations — all optional.
	CreateQueue   *ManageAction
	DeleteQueue   *ManageAction
	CreateTopic   *ManageAction
	DeleteTopic   *ManageAction
	CreateExchange *ManageAction
	DeleteExchange *ManageAction
	BindQueue      *BindAction
	UnbindQueue    *BindAction
}

// NewManageCommand builds a standardised "manage" command tree from spec,
// adding only the subcommands the broker implements.
func NewManageCommand(spec ManageSpec) *cobra.Command {
	mgmtCmd := &cobra.Command{
		Use:   "manage",
		Short: "Broker management operations",
	}

	if spec.SetupFlags != nil {
		spec.SetupFlags(mgmtCmd)
	}

	hasBoth := spec.ListQueues != nil && spec.ListTopics != nil

	if spec.ListQueues != nil || spec.ListTopics != nil {
		mgmtCmd.AddCommand(&cobra.Command{
			Use:   "list",
			Short: "List queues and topics",
			RunE: func(c *cobra.Command, args []string) error {
				if spec.ListQueues != nil {
					queues, err := spec.ListQueues()
					if err != nil {
						return err
					}
					for _, q := range queues {
						if hasBoth {
							fmt.Printf("queue  %-40s  messages=%d\n", q.Name, q.MessageCount)
						} else {
							fmt.Printf("%-40s  messages=%d\n", q.Name, q.MessageCount)
						}
					}
				}
				if spec.ListTopics != nil {
					topics, err := spec.ListTopics()
					if err != nil {
						return err
					}
					for _, t := range topics {
						if hasBoth {
							fmt.Printf("topic  %s\n", t.Name)
						} else {
							fmt.Printf("%s\n", t.Name)
						}
					}
				}
				return nil
			},
		})
	}

	if spec.Purge != nil {
		mgmtCmd.AddCommand(&cobra.Command{
			Use:   "purge <queue>",
			Short: "Remove all messages from a queue",
			Args:  cobra.ExactArgs(1),
			RunE: func(c *cobra.Command, args []string) error {
				count, err := spec.Purge(args[0])
				if err != nil {
					return err
				}
				if count > 0 {
					fmt.Printf("Purged %d messages from %s\n", count, args[0])
				} else {
					fmt.Printf("Purged queue %s\n", args[0])
				}
				return nil
			},
		})
	}

	if spec.Stats != nil {
		mgmtCmd.AddCommand(&cobra.Command{
			Use:   "stats <queue>",
			Short: "Show queue statistics",
			Args:  cobra.ExactArgs(1),
			RunE: func(c *cobra.Command, args []string) error {
				stats, err := spec.Stats(args[0])
				if err != nil {
					return err
				}
				fmt.Printf("Queue:     %s\n", stats.Name)
				fmt.Printf("Messages:  %d\n", stats.MessageCount)
				fmt.Printf("Consumers: %d\n", stats.ConsumerCount)
				if stats.EnqueueCount > 0 {
					fmt.Printf("Enqueued:  %d\n", stats.EnqueueCount)
				}
				if stats.DequeueCount > 0 {
					fmt.Printf("Dequeued:  %d\n", stats.DequeueCount)
				}
				return nil
			},
		})
	}

	addManageAction(mgmtCmd, "create-queue", "Create a queue", "<queue>", "Created queue %s\n", spec.CreateQueue)
	addManageAction(mgmtCmd, "delete-queue", "Delete a queue", "<queue>", "Deleted queue %s\n", spec.DeleteQueue)
	addManageAction(mgmtCmd, "create-topic", "Create a topic", "<topic>", "Created topic %s\n", spec.CreateTopic)
	addManageAction(mgmtCmd, "delete-topic", "Delete a topic", "<topic>", "Deleted topic %s\n", spec.DeleteTopic)
	addManageAction(mgmtCmd, "create-exchange", "Create an exchange", "<exchange>", "Created exchange %s\n", spec.CreateExchange)
	addManageAction(mgmtCmd, "delete-exchange", "Delete an exchange", "<exchange>", "Deleted exchange %s\n", spec.DeleteExchange)
	addBindAction(mgmtCmd, "bind-queue", "Bind a queue to an exchange", "Bound queue %s to exchange %s\n", spec.BindQueue)
	addBindAction(mgmtCmd, "unbind-queue", "Unbind a queue from an exchange", "Unbound queue %s from exchange %s\n", spec.UnbindQueue)

	return mgmtCmd
}

// addManageAction adds a single-arg management subcommand (create/delete) if
// the action is non-nil.
func addManageAction(parent *cobra.Command, use, short, argName, successFmt string, action *ManageAction) {
	if action == nil {
		return
	}
	c := &cobra.Command{
		Use:   use + " " + argName,
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			if err := action.Run(args[0]); err != nil {
				return err
			}
			fmt.Printf(successFmt, args[0])
			return nil
		},
	}
	if action.SetupFlags != nil {
		action.SetupFlags(c)
	}
	parent.AddCommand(c)
}

// addBindAction adds a two-arg management subcommand (bind/unbind) if the
// action is non-nil.
func addBindAction(parent *cobra.Command, use, short, successFmt string, action *BindAction) {
	if action == nil {
		return
	}
	c := &cobra.Command{
		Use:   use + " <queue> <exchange>",
		Short: short,
		Args:  cobra.ExactArgs(2),
		RunE: func(c *cobra.Command, args []string) error {
			if err := action.Run(args[0], args[1]); err != nil {
				return err
			}
			fmt.Printf(successFmt, args[0], args[1])
			return nil
		},
	}
	if action.SetupFlags != nil {
		action.SetupFlags(c)
	}
	parent.AddCommand(c)
}
