package cmd

import (
	"time"

	"github.com/makibytes/xmc/broker/backends"
	"github.com/makibytes/xmc/log"
	"github.com/spf13/cobra"
)

// TargetSpec holds the parsed routing flags (-e/-q/<to>) passed to
// a broker's ResolveTarget function.
type TargetSpec struct {
	IsTopic  bool   // true for publish/subscribe, false for send/receive
	To       string // positional <to> argument (may be empty)
	Exchange string // -e/--exchange value (may be empty)
	Queue    string // -q/--queue value (may be empty)
}

// TargetResolver maps the routing flags into a single broker destination
// string. Only exchange-routed brokers (e.g. RabbitMQ) implement this.
type TargetResolver func(TargetSpec) (string, error)

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

	// ResolveTarget maps <to> (and optionally -e/-q) into a destination
	// string for produce/consume commands. When nil, the positional argument
	// is used directly. When set, the resolver determines the destination
	// from the positional and any registered routing flags.
	ResolveTarget TargetResolver

	// ExchangeRouting controls whether -e/--exchange and -q/--queue flags
	// are registered on send/publish/receive/subscribe. Only brokers with
	// exchange-based routing (e.g. RabbitMQ) set this to true. When false,
	// a ResolveTarget is still called (if set) but only receives the bare
	// positional <to>.
	ExchangeRouting bool

	// ProduceFlags registers broker-specific flags on send and publish
	// commands (e.g. --qos, --retain, --fifo, --anycast).
	ProduceFlags func(c *cobra.Command)

	// ConsumeFlags registers broker-specific flags on receive, peek, and
	// subscribe commands (e.g. --qos, --subscription, --visibility-timeout).
	ConsumeFlags func(c *cobra.Command)

	// ProduceExtra reads broker-specific produce flags back from a parsed
	// command and returns them as key-value pairs for SendOptions/PublishOptions.Extra.
	ProduceExtra func(c *cobra.Command) map[string]string

	// ConsumeExtra reads broker-specific consume flags back from a parsed
	// command and returns them as key-value pairs for ReceiveOptions/SubscribeOptions.Extra.
	ConsumeExtra func(c *cobra.Command) map[string]string

	// Ping creates a short-lived connection to verify broker reachability.
	Ping Connector

	// Manage is an optional pre-built "manage" command (see NewManageCommand).
	// Deprecated: set ManageSpec instead — a fresh command is built per shell
	// invocation so that IO routing and arg state are clean.
	Manage *cobra.Command

	// ManageSpec describes the broker's management capabilities. When set,
	// NewRootCommand builds the "manage" command from it (and the shell can
	// rebuild a fresh one per pipeline invocation).
	ManageSpec *ManageSpec

	// Extra holds broker-specific commands that don't fit the standard set
	// (e.g. Kafka's forward-topic, Artemis's MCP server).
	Extra []*cobra.Command

	// AIContext is optional broker-specific documentation (typically embedded
	// from docs/<broker>.md) that is injected into the AI mode system prompt
	// so the model understands broker-specific concepts (e.g. exchanges,
	// ANYCAST/MULTICAST, JetStream streams, etc.).
	AIContext string
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
			applyConfigFallback(cmd)
		},
	}

	rootCmd.PersistentFlags().BoolP("verbose", "v", false, "Print verbose output")
	rootCmd.PersistentFlags().Bool("reconnect", false, "Auto-reconnect on connection loss (enabled by default in shell mode)")
	rootCmd.PersistentFlags().Duration("reconnect-window", 5*time.Minute, "Maximum time to keep retrying before giving up")

	if spec.RegisterFlags != nil {
		spec.RegisterFlags(rootCmd)
	}

	// Wrap the adapter factories so that --reconnect is checked at call time:
	// if set, the factory returns a reconnecting adapter; otherwise the plain
	// adapter. This avoids reopening a connection just to query the flag.
	queueFactory := conditionalReconnectQueue(spec.Queue, rootCmd)
	topicFactory := conditionalReconnectTopic(spec.Topic, rootCmd)

	// Queue commands.
	if spec.Queue != nil {
		resolver := spec.ResolveTarget
		exchRouting := spec.ExchangeRouting
		produceFlags := spec.ProduceFlags
		consumeFlags := spec.ConsumeFlags
		produceExtra := spec.ProduceExtra
		consumeExtra := spec.ConsumeExtra
		rootCmd.AddCommand(WrapQueueCommand(func(b backends.QueueBackend) *cobra.Command {
			c := NewSendCommand(b, resolver, produceExtra, exchRouting)
			if produceFlags != nil {
				produceFlags(c)
			}
			return c
		}, queueFactory))
		rootCmd.AddCommand(WrapQueueCommand(func(b backends.QueueBackend) *cobra.Command {
			c := NewReceiveCommand(b, resolver, consumeExtra, exchRouting)
			if consumeFlags != nil {
				consumeFlags(c)
			}
			return c
		}, queueFactory))
		rootCmd.AddCommand(WrapQueueCommand(func(b backends.QueueBackend) *cobra.Command {
			c := NewPeekCommand(b)
			if consumeFlags != nil {
				consumeFlags(c)
			}
			return c
		}, queueFactory))
		// Other queue commands don't need routing or extra flags.
		for _, newCmd := range []func(backends.QueueBackend) *cobra.Command{
			NewRequestCommand,
			NewReplyCommand,
			NewMoveCommand,
		} {
			rootCmd.AddCommand(WrapQueueCommand(newCmd, queueFactory))
		}
	}

	// Topic commands.
	if spec.Topic != nil {
		resolver := spec.ResolveTarget
		exchRouting := spec.ExchangeRouting
		produceFlags := spec.ProduceFlags
		consumeFlags := spec.ConsumeFlags
		produceExtra := spec.ProduceExtra
		consumeExtra := spec.ConsumeExtra
		rootCmd.AddCommand(WrapTopicCommand(func(b backends.TopicBackend) *cobra.Command {
			c := NewPublishCommand(b, resolver, produceExtra, exchRouting)
			if produceFlags != nil {
				produceFlags(c)
			}
			return c
		}, topicFactory))
		rootCmd.AddCommand(WrapTopicCommand(func(b backends.TopicBackend) *cobra.Command {
			c := NewSubscribeCommand(b, resolver, consumeExtra, exchRouting)
			if consumeFlags != nil {
				consumeFlags(c)
			}
			return c
		}, topicFactory))
	}

	// forward/bridge can relay queue<->queue, queue<->topic, or topic<->topic
	// (topology chosen via flags), so each is registered once here rather than
	// once per messaging model — a queue-named and topic-named command sharing
	// the same name would silently shadow one another (see
	// project-topic-queue-command-naming memory / NewRootCommand callers).
	if spec.Queue != nil || spec.Topic != nil {
		rootCmd.AddCommand(WrapForwardCommand(queueFactory, topicFactory))
		rootCmd.AddCommand(WrapBridgeCommand(queueFactory, topicFactory))
	}

	// Management — prefer ManageSpec (fresh command per invocation in the shell)
	// but fall back to a pre-built Manage command for backwards compatibility.
	if spec.ManageSpec != nil && spec.Manage == nil {
		spec.Manage = NewManageCommand(*spec.ManageSpec)
	}
	if spec.Manage != nil {
		rootCmd.AddCommand(spec.Manage)
	}

	// Extra broker-specific commands.
	for _, extra := range spec.Extra {
		rootCmd.AddCommand(extra)
	}

	// Interactive shell and AI mode.
	rootCmd.AddCommand(NewShellCommand(spec))
	rootCmd.AddCommand(NewAICommand(spec))

	// Connectivity check.
	if spec.Ping != nil {
		rootCmd.AddCommand(NewPingCommand(spec.Ping))
	}

	rootCmd.AddCommand(NewVersionCommand())

	return rootCmd
}

// applyConfigFallback loads the YAML config file and fills in empty connection
// flags (server, user, password) from it. Precedence: flag > env var > YAML.
// A flag is only overridden if it was not explicitly set and its current value
// (from env var or built-in default) is empty.
func applyConfigFallback(cmd *cobra.Command) {
	cfg, err := loadConfig()
	if err != nil {
		log.Verbose("config: %s", err)
		return
	}
	fallbacks := map[string]string{
		"server":   cfg.Connection.Server,
		"user":     cfg.Connection.User,
		"password": cfg.Connection.Password,
	}
	for name, val := range fallbacks {
		if val == "" {
			continue
		}
		f := cmd.Root().Flags().Lookup(name)
		if f == nil {
			f = cmd.Root().PersistentFlags().Lookup(name)
		}
		if f == nil {
			continue
		}
		if f.Changed {
			continue
		}
		if f.Value.String() != "" {
			continue
		}
		_ = f.Value.Set(val)
	}
}
