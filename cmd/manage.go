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

// BindAction is a binding subcommand taking <queue> <target> (+ optional
// flags like --routing-key bound in SetupFlags). TargetNoun names the second
// argument in help and success text; it defaults to "exchange" (RabbitMQ) and
// is "address" for Artemis.
type BindAction struct {
	SetupFlags func(c *cobra.Command) // optional
	Run        func(queue, target string) error
	TargetNoun string // optional; defaults to "exchange"
}

// targetNoun returns the noun for the bind target, defaulting to "exchange".
func (a *BindAction) targetNoun() string {
	if a != nil && a.TargetNoun != "" {
		return a.TargetNoun
	}
	return "exchange"
}

// ObjectType declares one browsable object type for the AI TUI sidebar. Each
// broker fills in its own set (e.g. RabbitMQ: Queues, Exchanges; Kafka: Topics,
// Consumer Groups). Hierarchical types (exchange→binding→queue) populate
// Children on the returned nodes when expanded.
type ObjectType struct {
	Label        string                                // window title: "Queues", "Exchanges", "Subscriptions"
	Hierarchical bool                                  // true → expand hotkey reveals Children as a tree
	List         func() ([]backends.ObjectNode, error) // returns the current objects
}

// SidebarActions returns the create and delete ManageAction for a given object
// label, derived from the ManageSpec. Returns nil for both when the label has no
// matching action (e.g. "Consumer Groups"). The mapping follows broker conventions:
//
//	"Queues" → CreateQueue / DeleteQueue
//	"Topics" → CreateTopic / DeleteTopic
//	"Addresses" → CreateAddress / DeleteAddress
//	"Exchanges" → CreateExchange / DeleteExchange
//	"Streams" → CreateQueue / DeleteQueue (NATS)
//	"Consumer Groups" → nil / DeleteConsumerGroup (Kafka; groups are created
//	  implicitly by the first consumer to join, so there is no create action)
func (s *ManageSpec) SidebarActions(label string) (create, delete *ManageAction) {
	switch label {
	case "Queues":
		return s.CreateQueue, s.DeleteQueue
	case "Topics":
		return s.CreateTopic, s.DeleteTopic
	case "Addresses":
		return s.CreateAddress, s.DeleteAddress
	case "Exchanges":
		return s.CreateExchange, s.DeleteExchange
	case "Streams":
		return s.CreateQueue, s.DeleteQueue
	case "Consumer Groups":
		return nil, s.DeleteConsumerGroup
	}
	return nil, nil
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
	// PurgeSubscription removes all messages from a topic's subscription
	// (a genuine message-storing object, not a routing pointer — currently
	// Azure Service Bus and Google Pub/Sub only) and returns the count
	// removed (or 0 when the broker does not report a count). topic is the
	// parent topic's name; some brokers need it to resolve the subscription
	// (Azure — same subscription name may exist under different topics),
	// others ignore it (Google — subscription names are globally unique).
	PurgeSubscription func(topic, subscription string) (int64, error)
	// Stats returns detailed statistics for a single queue.
	Stats func(queue string) (*backends.QueueStats, error)
	// SetupFlags registers additional persistent flags on the manage command
	// (e.g. Pulsar's --admin-port).
	SetupFlags func(cmd *cobra.Command)

	// Resource lifecycle operations — all optional.
	CreateQueue    *ManageAction
	DeleteQueue    *ManageAction
	UpdateQueue    *ManageAction // change settings of an existing queue (e.g. Artemis filter)
	EnableQueue    *ManageAction // enable message dispatch on a queue (Artemis)
	DisableQueue   *ManageAction // disable message dispatch on a queue (Artemis)
	CreateTopic    *ManageAction
	DeleteTopic    *ManageAction
	UpdateTopic    *ManageAction // change settings of an existing topic (e.g. Kafka partitions/config)
	CreateAddress  *ManageAction // Artemis-only: bare address (routing namespace)
	DeleteAddress  *ManageAction // Artemis-only: bare address
	CreateExchange *ManageAction
	DeleteExchange *ManageAction
	BindQueue      *BindAction
	UnbindQueue    *BindAction

	// DeleteConsumerGroup deletes an inactive consumer group (Kafka). There is
	// no matching create action: Kafka creates groups implicitly the moment a
	// consumer with that group.id first joins, and has no API to pre-create one.
	DeleteConsumerGroup *ManageAction
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

	if spec.PurgeSubscription != nil {
		mgmtCmd.AddCommand(&cobra.Command{
			Use:   "purge-subscription <topic> <subscription>",
			Short: "Remove all messages from a topic subscription",
			Args:  cobra.ExactArgs(2),
			RunE: func(c *cobra.Command, args []string) error {
				w := c.OutOrStdout()
				count, err := spec.PurgeSubscription(args[0], args[1])
				if err != nil {
					return err
				}
				if count > 0 {
					fmt.Fprintf(w, "Purged %d messages from subscription %s on topic %s\n", count, args[1], args[0])
				} else {
					fmt.Fprintf(w, "Purged subscription %s on topic %s\n", args[1], args[0])
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
	addManageAction(mgmtCmd, "update-queue", "Update queue settings", "<queue>", "Updated queue %s\n", spec.UpdateQueue)
	addManageAction(mgmtCmd, "enable-queue", "Enable message dispatch on a queue", "<queue>", "Enabled queue %s\n", spec.EnableQueue)
	addManageAction(mgmtCmd, "disable-queue", "Disable message dispatch on a queue", "<queue>", "Disabled queue %s\n", spec.DisableQueue)
	addManageAction(mgmtCmd, "create-topic", "Create a topic", "<topic>", "Created topic %s\n", spec.CreateTopic)
	addManageAction(mgmtCmd, "delete-topic", "Delete a topic", "<topic>", "Deleted topic %s\n", spec.DeleteTopic)
	addManageAction(mgmtCmd, "update-topic", "Update topic settings", "<topic>", "Updated topic %s\n", spec.UpdateTopic)
	addManageAction(mgmtCmd, "create-address", "Create an address (routing namespace)", "<address>", "Created address %s\n", spec.CreateAddress)
	addManageAction(mgmtCmd, "delete-address", "Delete an address", "<address>", "Deleted address %s\n", spec.DeleteAddress)
	addManageAction(mgmtCmd, "create-exchange", "Create an exchange", "<exchange>", "Created exchange %s\n", spec.CreateExchange)
	addManageAction(mgmtCmd, "delete-exchange", "Delete an exchange", "<exchange>", "Deleted exchange %s\n", spec.DeleteExchange)
	addBindAction(mgmtCmd, "bind-queue", "Bind a queue to an %s", "Bound queue %%s to %s %%s\n", spec.BindQueue)
	addBindAction(mgmtCmd, "unbind-queue", "Unbind a queue from an %s", "Unbound queue %%s from %s %%s\n", spec.UnbindQueue)
	addManageAction(mgmtCmd, "delete-consumer-group", "Delete a consumer group", "<group>", "Deleted consumer group %s\n", spec.DeleteConsumerGroup)

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
// action is non-nil. shortFmt and successFmt contain a %s placeholder for the
// action's target noun ("exchange" by default, "address" for Artemis).
func addBindAction(parent *cobra.Command, use, shortFmt, successFmt string, action *BindAction) {
	if action == nil {
		return
	}
	noun := action.targetNoun()
	successFmt = fmt.Sprintf(successFmt, noun)
	c := &cobra.Command{
		Use:   fmt.Sprintf("%s <queue> <%s>", use, noun),
		Short: fmt.Sprintf(shortFmt, noun),
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
