//go:build artemis

package broker

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/makibytes/xmc/broker/artemis"
	"github.com/makibytes/xmc/broker/backends"
	"github.com/makibytes/xmc/cmd"
	"github.com/makibytes/xmc/mcp"
	"github.com/spf13/cobra"
)

func GetRootCommand() *cobra.Command {
	var connArgs artemis.ConnArguments

	defaultServer := os.Getenv("AMC_SERVER")
	if defaultServer == "" {
		defaultServer = "amqp://localhost:5672"
	}

	var brokerName string
	var addrRoutingType string

	// Commands whose Run needs Flags().Changed() capture their own *cobra.Command
	// here (SetupFlags runs at build time, Run at exec time of the same instance).
	var createQueueCmd, updateQueueCmd, bindQueueCmd *cobra.Command

	mgmtArgs := func() artemis.ManagementArgs {
		return artemis.ManagementArgs{Server: connArgs.Server, User: connArgs.User, Password: connArgs.Password, BrokerName: brokerName}
	}

	return cmd.NewRootCommand(cmd.BrokerSpec{
		Use:       "amc",
		Short:     "Apache Artemis Messaging Client",
		Long:      "Command-line interface for Apache Artemis messaging",
		AIContext: AIDoc("artemis"),
		ProduceFlags: func(c *cobra.Command) {
			c.Flags().Bool("anycast", false, "Force ANYCAST routing type")
			c.Flags().Bool("multicast", false, "Force MULTICAST routing type")
		},
		ProduceExtra: func(c *cobra.Command) map[string]string {
			extra := make(map[string]string)
			if ac, _ := c.Flags().GetBool("anycast"); ac {
				extra["routing-type"] = "anycast"
			}
			if mc, _ := c.Flags().GetBool("multicast"); mc {
				extra["routing-type"] = "multicast"
			}
			return extra
		},
		RegisterFlags: func(c *cobra.Command) {
			c.PersistentFlags().StringVarP(&connArgs.Server, "server", "s", defaultServer, "Server URL")
			c.PersistentFlags().StringVarP(&connArgs.User, "user", "u", os.Getenv("AMC_USER"), "Username for SASL PLAIN login")
			c.PersistentFlags().StringVarP(&connArgs.Password, "password", "p", os.Getenv("AMC_PASSWORD"), "Password for SASL PLAIN login")
			c.PersistentFlags().StringVar(&brokerName, "broker-name", "", "Artemis broker name for Jolokia management")
			backends.RegisterTLSFlags(c, &connArgs.TLS)
		},
		Queue: func() (backends.QueueBackend, error) { return artemis.NewQueueAdapter(connArgs) },
		Topic: func() (backends.TopicBackend, error) { return artemis.NewTopicAdapter(connArgs) },
		Ping:  func() (cmd.Closeable, error) { return artemis.NewQueueAdapter(connArgs) },
		ManageSpec: &cmd.ManageSpec{
			Objects: []cmd.ObjectType{
				{
					Label: "Queues",
					List: func() ([]backends.ObjectNode, error) {
						queues, err := artemis.ListQueues(mgmtArgs())
						if err != nil {
							return nil, err
						}
						out := make([]backends.ObjectNode, len(queues))
						for i, q := range queues {
							out[i] = backends.ObjectNode{
								Name:    q.Name,
								Metrics: []backends.Metric{{Label: "msgs", Value: q.MessageCount}, {Label: "consumers", Value: int64(q.ConsumerCount)}},
							}
						}
						return out, nil
					},
				},
				{
					Label: "Addresses",
					List: func() ([]backends.ObjectNode, error) {
						return artemis.ListAddresses(mgmtArgs())
					},
				},
			},
			Purge: func(queue string) (int64, error) { return artemis.PurgeQueue(mgmtArgs(), queue) },
			Stats: func(queue string) (*backends.QueueStats, error) {
				stats, err := artemis.GetQueueStats(mgmtArgs(), queue)
				if err != nil {
					return nil, err
				}
				return &backends.QueueStats{
					Name: stats.Name, MessageCount: stats.MessageCount,
					ConsumerCount: stats.ConsumerCount, EnqueueCount: stats.EnqueueCount,
					DequeueCount: stats.DequeueCount,
				}, nil
			},
			CreateQueue: &cmd.ManageAction{
				SetupFlags: func(c *cobra.Command) {
					createQueueCmd = c
					registerQueueSettingsFlags(c)
					c.Flags().String("address", "", "Address to bind the queue to (default: queue name)")
					c.Flags().String("routing-type", "anycast", "Routing type: anycast or multicast")
					c.Flags().Bool("durable", true, "Queue survives a broker restart")
					c.Flags().Bool("last-value", false, "Keep only the latest message per last-value key")
					c.Flags().String("last-value-key", "", "Message property used as the last-value key")
					c.Flags().Int64("ring-size", -1, "Keep only the newest N messages (ring queue)")
				},
				Run: func(queue string) error {
					return artemis.CreateQueueWithConfig(mgmtArgs(), queueConfigFromFlags(createQueueCmd, queue))
				},
			},
			DeleteQueue: &cmd.ManageAction{Run: func(queue string) error { return artemis.DeleteQueue(mgmtArgs(), queue) }},
			UpdateQueue: &cmd.ManageAction{
				SetupFlags: func(c *cobra.Command) {
					updateQueueCmd = c
					registerQueueSettingsFlags(c)
					c.Flags().Int64("ring-size", -1, "Keep only the newest N messages (ring queue)")
				},
				Run: func(queue string) error {
					cfg := queueConfigFromFlags(updateQueueCmd, queue)
					if len(cfg) == 1 { // only "name" — nothing to change
						return fmt.Errorf("no settings given; pass at least one flag (e.g. --filter)")
					}
					return artemis.UpdateQueueConfig(mgmtArgs(), cfg)
				},
			},
			EnableQueue:  &cmd.ManageAction{Run: func(queue string) error { return artemis.EnableQueue(mgmtArgs(), queue) }},
			DisableQueue: &cmd.ManageAction{Run: func(queue string) error { return artemis.DisableQueue(mgmtArgs(), queue) }},
			BindQueue: &cmd.BindAction{
				TargetNoun: "address",
				SetupFlags: func(c *cobra.Command) {
					bindQueueCmd = c
					c.Long = `In Artemis a queue is bound to exactly one address at creation time and
cannot be re-bound. bind-queue therefore creates <queue> under <address>
(creating the address when it does not exist). To move a queue to another
address, delete it and bind it again.`
					c.Flags().String("routing-type", "anycast", "Routing type: anycast or multicast")
					c.Flags().String("filter", "", "Only route messages matching this filter expression to the queue")
					c.Flags().Bool("durable", true, "Queue survives a broker restart")
				},
				Run: func(queue, address string) error {
					cfg := queueConfigFromFlags(bindQueueCmd, queue)
					cfg["address"] = address
					return artemis.CreateQueueWithConfig(mgmtArgs(), cfg)
				},
			},
			CreateTopic: &cmd.ManageAction{Run: func(topic string) error { return artemis.CreateTopic(mgmtArgs(), topic) }},
			DeleteTopic: &cmd.ManageAction{Run: func(topic string) error { return artemis.DeleteTopic(mgmtArgs(), topic) }},
			CreateAddress: &cmd.ManageAction{
				SetupFlags: func(c *cobra.Command) {
					c.Flags().StringVar(&addrRoutingType, "routing-type", "ANYCAST", "Routing type: ANYCAST, MULTICAST, or ANYCAST,MULTICAST")
				},
				Run: func(name string) error { return artemis.CreateAddress(mgmtArgs(), name, addrRoutingType) },
			},
			DeleteAddress: &cmd.ManageAction{Run: func(name string) error { return artemis.DeleteAddress(mgmtArgs(), name) }},
		},
		Extra: []*cobra.Command{
			mcp.NewCommand(mcp.Deps{
				ServerName:    "xmc-artemis",
				ServerVersion: cmd.Version(),
				Target:        connArgs.Server,
				NewQueue: func() (backends.QueueBackend, error) {
					return artemis.NewQueueAdapter(connArgs)
				},
				NewTopic: func() (backends.TopicBackend, error) {
					return artemis.NewTopicAdapter(connArgs)
				},
				ListQueues: func(ctx context.Context) ([]mcp.QueueInfo, error) {
					queues, err := artemis.ListQueues(mgmtArgs())
					if err != nil {
						return nil, err
					}
					out := make([]mcp.QueueInfo, 0, len(queues))
					for _, q := range queues {
						out = append(out, mcp.QueueInfo{
							Name: q.Name, RoutingType: q.RoutingType,
							MessageCount: int64(q.MessageCount), ConsumerCount: int64(q.ConsumerCount),
						})
					}
					return out, nil
				},
				PurgeQueue: func(ctx context.Context, queue string) (int64, error) {
					return artemis.PurgeQueue(mgmtArgs(), queue)
				},
				QueueStats: func(ctx context.Context, queue string) (*mcp.QueueStats, error) {
					stats, err := artemis.GetQueueStats(mgmtArgs(), queue)
					if err != nil {
						return nil, err
					}
					return &mcp.QueueStats{
						Name: stats.Name, MessageCount: int64(stats.MessageCount),
						ConsumerCount: int64(stats.ConsumerCount), EnqueueCount: int64(stats.EnqueueCount),
						DequeueCount: int64(stats.DequeueCount),
					}, nil
				},
			}),
		},
	})
}

// registerQueueSettingsFlags registers the queue settings shared by
// create-queue and update-queue. Durability is not here: it is fixed at
// creation time (the broker silently ignores it on updateQueue), so only
// create-queue and bind-queue register --durable.
func registerQueueSettingsFlags(c *cobra.Command) {
	c.Flags().String("filter", "", "Only route messages matching this filter expression to the queue (\"\" removes the filter)")
	c.Flags().Int("max-consumers", -1, "Maximum concurrent consumers (-1 = unlimited)")
	c.Flags().Bool("purge-on-no-consumers", false, "Delete pending messages when the last consumer disconnects")
	c.Flags().Bool("exclusive", false, "Deliver all messages to a single consumer at a time")
	c.Flags().Bool("non-destructive", false, "Consuming leaves messages on the queue")
}

// queueConfigFromFlags builds the QueueConfiguration attribute map from the
// flags the user actually set on c; unset flags are omitted so broker defaults
// apply (and update-queue leaves them unchanged). Shared by create-queue,
// update-queue, and bind-queue, which register different flag subsets.
func queueConfigFromFlags(c *cobra.Command, queue string) artemis.QueueConfig {
	cfg := artemis.QueueConfig{"name": queue}
	f := c.Flags()
	set := func(flag, key string, value func() any) {
		if fl := f.Lookup(flag); fl != nil && fl.Changed {
			cfg[key] = value()
		}
	}
	set("address", "address", func() any { v, _ := f.GetString("address"); return v })
	set("routing-type", "routing-type", func() any { v, _ := f.GetString("routing-type"); return strings.ToUpper(v) })
	set("filter", "filter-string", func() any { v, _ := f.GetString("filter"); return v })
	set("durable", "durable", func() any { v, _ := f.GetBool("durable"); return v })
	set("max-consumers", "max-consumers", func() any { v, _ := f.GetInt("max-consumers"); return v })
	set("purge-on-no-consumers", "purge-on-no-consumers", func() any { v, _ := f.GetBool("purge-on-no-consumers"); return v })
	set("exclusive", "exclusive", func() any { v, _ := f.GetBool("exclusive"); return v })
	set("last-value", "last-value", func() any { v, _ := f.GetBool("last-value"); return v })
	set("last-value-key", "last-value-key", func() any { v, _ := f.GetString("last-value-key"); return v })
	set("non-destructive", "non-destructive", func() any { v, _ := f.GetBool("non-destructive"); return v })
	set("ring-size", "ring-size", func() any { v, _ := f.GetInt64("ring-size"); return v })
	return cfg
}
