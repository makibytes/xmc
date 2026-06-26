package cmd

import (
	"fmt"
	"strings"

	"github.com/makibytes/xmc/broker/backends"
	"github.com/spf13/cobra"
)

// formatMetrics formats a node's metrics as "label=value" pairs for text output.
func formatMetrics(metrics []backends.Metric) string {
	if len(metrics) == 0 {
		return ""
	}
	parts := make([]string, len(metrics))
	for i, m := range metrics {
		parts[i] = fmt.Sprintf("%s=%d", m.Label, m.Value)
	}
	return strings.Join(parts, " ")
}

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

// ObjectType declares one browsable object type for the AI TUI sidebar. Each
// broker fills in its own set (e.g. RabbitMQ: Queues, Exchanges; Kafka: Topics,
// Consumer Groups). Hierarchical types (exchange→binding→queue) populate
// Children on the returned nodes when expanded.
type ObjectType struct {
	Label        string                                      // window title: "Queues", "Exchanges", "Subscriptions"
	Hierarchical bool                                        // true → expand hotkey reveals Children as a tree
	List         func() ([]backends.ObjectNode, error)       // returns the current objects
}

// ManageSpec describes the management capabilities a broker exposes. Each
// closure is optional — nil means the broker does not support that operation,
// and the corresponding subcommand is omitted.
type ManageSpec struct {
	// Objects declares the browsable object types shown in the AI TUI sidebar.
	// Each entry becomes a window. Order determines the display order.
	Objects []ObjectType

	// Purge removes all messages from a queue and returns the count removed
	// (or 0 when the broker does not report a count).
	Purge func(queue string) (int64, error)
	// Stats returns detailed statistics for a single queue.
	Stats func(queue string) (*backends.QueueStats, error)
	// SetupFlags registers additional persistent flags on the manage command
	// (e.g. Pulsar's --admin-port).
	SetupFlags func(cmd *cobra.Command)

	// Resource lifecycle operations — all optional.
	CreateQueue    *ManageAction
	DeleteQueue    *ManageAction
	CreateTopic    *ManageAction
	DeleteTopic    *ManageAction
	CreateAddress  *ManageAction // Artemis-only: bare address (routing namespace)
	DeleteAddress  *ManageAction // Artemis-only: bare address
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

	hasMultiple := len(spec.Objects) > 1

	if len(spec.Objects) > 0 {
		mgmtCmd.AddCommand(&cobra.Command{
			Use:   "list",
			Short: "List broker objects",
			RunE: func(c *cobra.Command, args []string) error {
				w := c.OutOrStdout()
				for _, ot := range spec.Objects {
					nodes, err := ot.List()
					if err != nil {
						return err
					}
					prefix := ""
					if hasMultiple {
						prefix = strings.ToLower(ot.Label) + "  "
					}
					for _, n := range nodes {
						metricStr := formatMetrics(n.Metrics)
						detail := strings.TrimSpace(n.Kind + " " + metricStr)
						if detail != "" {
							fmt.Fprintf(w, "%s%-40s  %s\n", prefix, n.Name, detail)
						} else {
							fmt.Fprintf(w, "%s%s\n", prefix, n.Name)
						}
						for _, child := range n.Children {
							childMetrics := formatMetrics(child.Metrics)
							kind := child.Kind
							if kind != "" {
								kind += " "
							}
							if childMetrics != "" {
								fmt.Fprintf(w, "%s  └ %s%s  %s\n", prefix, kind, child.Name, childMetrics)
							} else {
								fmt.Fprintf(w, "%s  └ %s%s\n", prefix, kind, child.Name)
							}
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
				w := c.OutOrStdout()
				count, err := spec.Purge(args[0])
				if err != nil {
					return err
				}
				if count > 0 {
					fmt.Fprintf(w, "Purged %d messages from %s\n", count, args[0])
				} else {
					fmt.Fprintf(w, "Purged queue %s\n", args[0])
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
				w := c.OutOrStdout()
				stats, err := spec.Stats(args[0])
				if err != nil {
					return err
				}
				fmt.Fprintf(w, "Queue:     %s\n", stats.Name)
				fmt.Fprintf(w, "Messages:  %d\n", stats.MessageCount)
				fmt.Fprintf(w, "Consumers: %d\n", stats.ConsumerCount)
				if stats.EnqueueCount > 0 {
					fmt.Fprintf(w, "Enqueued:  %d\n", stats.EnqueueCount)
				}
				if stats.DequeueCount > 0 {
					fmt.Fprintf(w, "Dequeued:  %d\n", stats.DequeueCount)
				}
				return nil
			},
		})
	}

	addManageAction(mgmtCmd, "create-queue", "Create a queue", "<queue>", "Created queue %s\n", spec.CreateQueue)
	addManageAction(mgmtCmd, "delete-queue", "Delete a queue", "<queue>", "Deleted queue %s\n", spec.DeleteQueue)
	addManageAction(mgmtCmd, "create-topic", "Create a topic", "<topic>", "Created topic %s\n", spec.CreateTopic)
	addManageAction(mgmtCmd, "delete-topic", "Delete a topic", "<topic>", "Deleted topic %s\n", spec.DeleteTopic)
	addManageAction(mgmtCmd, "create-address", "Create an address (routing namespace)", "<address>", "Created address %s\n", spec.CreateAddress)
	addManageAction(mgmtCmd, "delete-address", "Delete an address", "<address>", "Deleted address %s\n", spec.DeleteAddress)
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
			fmt.Fprintf(c.OutOrStdout(), successFmt, args[0])
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
			fmt.Fprintf(c.OutOrStdout(), successFmt, args[0], args[1])
			return nil
		},
	}
	if action.SetupFlags != nil {
		action.SetupFlags(c)
	}
	parent.AddCommand(c)
}
