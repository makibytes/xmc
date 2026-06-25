# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

XMC (Xenomorphic Message Client) is a unified command-line interface for sending and receiving messages to/from different Message and Streaming Brokers. The broker backend is selected at **build time** using Go build tags (`artemis`, `aws`, `azure`, `google`, `ibmmq`, `kafka`, `mqtt`, `nats`, `pulsar`, `rabbitmq`, `redis`). Each flavor produces its own binary (`amc`, `awsmc`, `azmc`, `gmc`, `imc`, `kmc`, `mmc`, `nmc`, `pmc`, `rmc`, `redmc`) with its own environment variable prefix. The goal is to support a common set of features across different protocols and brokers, comparable to the JMS API.

## Building

Build for a specific broker using build tags:

```bash
go build -tags artemis -o amc .    # Apache Artemis (AMQP 1.0)
go build -tags ibmmq -o imc .      # IBM MQ, but needs a container with the proprietary C files
go build -tags kafka -o kmc .      # Apache Kafka
go build -tags mqtt -o mmc .       # MQTT Brokers
go build -tags nats -o nmc .       # NATS / JetStream
go build -tags pulsar -o pmc .     # Apache Pulsar
go build -tags rabbitmq -o rmc .   # RabbitMQ v4+ (AMQP 1.0)
go build -tags redis -o redmc .    # Redis (Streams)
go build -tags google -o gmc .     # Google Cloud Pub/Sub
go build -tags aws -o awsmc .      # AWS SQS + SNS
go build -tags azure -o azmc .     # Azure Service Bus
```

Default build (without tags) will fail with "No broker loaded" error at runtime.
All builds shall be triggered automatically in github workflows.

## Testing

Unit tests (no broker required):

```bash
go test ./cmd/ ./log/ ./broker/backends/ ./broker/amqpcommon/
```

Integration tests use the bats testing framework (included in `test/bats/`) and require a local Artemis broker:

```bash
# Start Artemis with Docker:
docker run --name artemis -d -p 8161:8161 -p 5672:5672 apache/activemq-artemis:latest-alpine

# Run tests:
./run-tests.sh
```

Test files are in `test/*.bats`. Default credentials for Artemis: `artemis/artemis`.

## Architecture

### Build-Time Broker Selection

The project uses Go build tags to compile only one broker backend per binary:

- `broker/*.go` files (e.g., `artemis.go`, `kafka.go`) use `//go:build <tag>` to implement `GetRootCommand()` for their respective brokers
- `main.go` calls `broker.GetRootCommand()` which returns `nil` if no broker tag was specified
- Each broker implementation lives in `broker/<broker-name>/` subdirectory
- `broker/stub.go` has a negative build constraint that must list every tag; update it when adding a new broker

### Adapter Pattern

Each broker provides adapters implementing the backend interfaces:

- `QueueBackend` interface (`broker/backends/queue.go`) - for queue operations (send, receive, peek, request)
- `TopicBackend` interface (`broker/backends/topic.go`) - for topic operations (publish, subscribe)
- `ManageableBackend` interface (`broker/backends/queue.go`) - optional, for management operations (list, purge, stats)
- `ManageableTopicBackend` interface (`broker/backends/topic.go`) - optional, for topic management (list topics)

Entry point files (`broker/artemis.go`, etc.) fill in a `cmd.BrokerSpec` struct and call `cmd.NewRootCommand(spec)` which wires up all commands, flags, and the verbose toggle. Management subcommands are specified via `ManageSpec` on `BrokerSpec`; `NewRootCommand` builds the `manage` command tree from it, and the shell builds a fresh one per pipeline invocation for clean IO routing.

### Shared AMQP Code

Artemis and RabbitMQ both use AMQP 1.0. Shared logic is in `broker/amqpcommon/`:
- `connect.go` - AMQP connection with SASL authentication and TLS support
- `receive.go` - Message receive with timeout, acknowledge/release, selectors, and durable subscriptions
- `message.go` - AMQP message to backend Message conversion

Broker-specific differences (Artemis routing annotations, RabbitMQ exchange routing) remain in their respective packages.

### Shared Helpers

- `broker/backends/naming.go` - `RandomSuffix()` (crypto/rand hex) and `SubscriptionName(opts)` (group / durable / ephemeral naming convention shared by all cloud brokers)
- `broker/backends/properties.go` - `StringifyProps()`, `PropMessageID`/`PropCorrelationID`/`PropReplyTo`/`PropContentType` constants (the cross-broker metadata contract)
- `broker/backends/timeout.go` - `TimeoutDuration(timeout, wait)` (shared timeout semantics)
- `broker/backends/errors.go` - `ErrNoMessageAvailable` (shared no-message sentinel)
- `cmd/root.go` - `BrokerSpec` + `NewRootCommand()` (shared CLI skeleton for all entry files); `TargetResolver`/`TargetSpec` for broker-specific address resolution; `ExchangeRouting` to toggle `-e/-q` flags (RabbitMQ); `ProduceFlags`/`ConsumeFlags`/`ProduceExtra`/`ConsumeExtra` hooks for per-message broker-specific flags (e.g. QoS, FIFO, routing-type)
- `cmd/manage.go` - `ManageSpec` + `ObjectType` + `ManageAction` + `BindAction` + `NewManageCommand()` (shared manage subcommand builder with standardised output via `c.OutOrStdout()`; `ObjectType` declares browsable object types with generic `ObjectNode` list; supports list/purge/stats and create/delete for queues, topics, exchanges, and bindings; fresh command built per shell invocation for clean IO routing)
- `cmd/produce.go` - `registerProduceFlags`, `parseProduceFlags`, `runProduce` (shared producer logic for send/publish)
- `cmd/command.go` - `WrapQueueCommand`/`WrapTopicCommand` adapter factories with lazy connection
- `cmd/shell.go` - `NewShellCommand` — interactive REPL with persistent connection, readline, history, deep cobra-tree autocomplete (subcommands + flags), `help <verb>`
- `cmd/aicmd.go` - `NewAICommand` — standalone AI shell command (builds its own session, runs the Bubble Tea TUI directly)
- `cmd/pipeline.go` - pipeline parser/executor: split, classify verb vs external, coalesce, wire `os.Pipe`, run via `errgroup`; semicolon-separated commands run sequentially (stop on first error)
- `cmd/reconnect.go` - `reconnectingQueue`/`reconnectingTopic` wrappers with capped exponential backoff (`cenkalti/backoff/v4`); `isConnectionError` classifies errors so only genuine transport failures trigger reconnect
- `cmd/ai.go` - `aiSession`, conversation history, predicates (`isDestructive`, `mutatesObjects`, `mutatesMessages`, `isManageList`), feedback loop, topology refresh
- `cmd/aitui.go` - Full-screen Bubble Tea TUI for AI shell: viewport (scrollable transcript), dual-mode input (`ask>` AI / `<binary>>` direct command, toggled with Esc), Tab autocomplete (reuses shell completer), Up/Down history recall (shared shell history file), spinner, streaming tokens, propose/edit/execute flow, auto-fix on error (re-proposes corrected command up to `maxFixAttempts`), N-window sidebar with per-broker object types (Shift+Tab browse, filter/sort/Space expand), inline command cards, connection probe with title-bar URL colouring
- `cmd/aiclient.go` - `aiClient` interface + provider implementations (Anthropic, OpenAI-compatible, Gemini) via stdlib `net/http`
- `cmd/aiconfig.go` - YAML config loading (`~/.xmc/<binary>.yml`), AI provider resolution with precedence order, `auto-update-objects`/`auto-update-messages` sidebar refresh config
- `cmd/aiprompt.go` - `buildCapabilities` (walks cobra tree), `systemPrompt` (strict syntax rules for the AI, pipeline framing, NDJSON schema), `extractCommand` (strips binary prefixes like `./rmc`, `rmc`, `xmc` so commands run in-process); broker-specific documentation is embedded from `docs/<broker>.md` via `broker.AIDoc()` into the system prompt — keep these compact (token-efficient reference cards, not tutorials)
- `cmd/aipaths.go` - config dir paths (`~/.xmc/` / `%LOCALAPPDATA%\xmc\`), per-binary file naming

### Command Structure

Uses `spf13/cobra` for CLI:
- Root command provides persistent flags (`--server`, `--user`, `--password`, `--verbose`, TLS flags)
- Environment variables prefixed per flavor: `AMC_` (Artemis), `IMC_` (IBM MQ), `KMC_` (Kafka), `MMC_` (MQTT), `NMC_` (NATS), `PMC_` (Pulsar), `RMC_` (RabbitMQ), `REDMC_` (Redis), `GMC_` (Google Pub/Sub), `AWSMC_` (AWS SQS+SNS), `AZMC_` (Azure Service Bus)
- Queue commands: `send`, `receive`, `peek`, `request`, `reply`, `move`, `forward`, `bridge`
- Topic commands: `publish`, `subscribe` (Kafka also has topic `forward`; topic-capable brokers also have topic `bridge`)
- Interactive: `shell`/`sh` (REPL with persistent connection, pipelines, auto-reconnect, deep autocomplete)
- AI shell: `ai` (standalone command — full-screen Bubble Tea TUI with natural-language → xmc command translation via LLM; dual input mode: `ask>` for AI prompts and `<binary>>` for direct xmc commands, toggled with Esc; Tab autocomplete in command mode; Up/Down history recall; connection probe with title-bar URL; N-window broker-object sidebar with Shift+Tab browse; inline command cards; shared shell history; quit via `/exit` or Ctrl+C)
- Configuration: `~/.xmc/<binary>.yml` YAML config for AI settings, broker-auth fallback (flag > env > YAML > default), sidebar auto-refresh (`auto-update-objects`, `auto-update-messages` — both default true), and command aliases (`aliases:` map with `$1`/`$2`/`$@` substitution)
- History: shared readline history per binary (`<binary>-sh.log` in `~/.xmc/`; AI-executed commands are appended to the same file)
- Connectivity: `ping` (all brokers; connects and reports reachability)
- Resilience: `--reconnect` (auto-reconnect with exponential backoff for long-running commands; only triggers on real connection/network errors, not application errors)
- Exchange routing (RabbitMQ): `-e`/`--exchange` and `-q`/`--queue` on send/publish (`--exchange`/`--queue-name` on receive/subscribe); defaults: send→`/queues/<to>`, publish→`/exchanges/amq.topic/<to>`; AMQP 1.0 v2 addresses always win
- Management commands: `manage list`, `manage purge`, `manage stats`, `manage create-queue`, `manage delete-queue`, `manage create-topic`, `manage delete-topic`, `manage create-exchange`, `manage delete-exchange`, `manage bind-queue`, `manage unbind-queue`
- Output: `-J` JSON, `-F`/`--format` template, or `--ndjson` lossless records, shared across read commands
- Bulk/load: `-l`/`--lines`, `--ndjson` (input), `-n`/`--count` repeat, `--rate` throttle on send/publish; `-n 0` drains on read commands
- Streaming: `forward` (continuous relay, optional `-x`/`--command` shell command), `bridge` (cross-broker relay via subprocess NDJSON streaming, `--to '<target command>'`), `--for <duration>` (time-bounded), `--stats` (live throughput) on read commands, `forward`, and `bridge`
- Connection parameters apply globally across all commands

### Message Handling

- **Application Properties**: Key-value metadata set with `-P key=value`
- **MessageProperties**: AMQP protocol-level properties (message-id, user-id, etc.), shown in verbose mode only
- **Data**: Message payload, can be passed as argument or via stdin
- Output to stdout omits newline when redirected for binary data preservation
- **JSON output** (`-J`): Structured JSON output for receive, peek, subscribe, and request commands
- **Line-delimited mode** (`-l`): Read stdin line by line, send/publish each line as separate message
- **MQTT limitation**: MQTT 3.1.1 has no user properties at the protocol level, so application properties and metadata (correlation-id, reply-to, content-type, message-id) cannot be carried through an MQTT broker

### TLS Support

All brokers support TLS via persistent flags (`--tls`, `--ca-cert`, `--cert`, `--key-file`, `--insecure`).
- AMQP brokers (Artemis, RabbitMQ): auto-detect `amqps://` URL scheme
- Kafka: auto-detect `kafka+ssl://` URL scheme
- MQTT: auto-detect `ssl://` URL scheme
- Pulsar: auto-detect `pulsar+ssl://` URL scheme
- Shared TLS config in `broker/amqpcommon/connect.go` for AMQP brokers
- Kafka has its own TLS config in `broker/kafka/connect.go`
- Cloud brokers (Google, AWS, Azure) and IBM MQ handle TLS internally and do not use the shared TLS flags

### Management APIs

Each broker uses its native management API. Object types listed via `ManageSpec.Objects` appear as sidebar windows in AI shell:
- **Artemis**: Jolokia REST API (HTTP port 8161) — queues, addresses; purge, stats, create/delete queue, create/delete topic
- **RabbitMQ**: RabbitMQ Management API (HTTP port 15672) — queues, exchanges (hierarchical: exchange→binding→queue); purge, stats, create/delete queue, create/delete exchange, bind/unbind queue
- **Kafka**: Admin client via `segmentio/kafka-go` — topics (partitions), consumer groups; create/delete topic (with `--partitions`, `--replication-factor`, `--config`)
- **IBM MQ**: No management commands (queue management via IBM tooling)
- **NATS**: JetStream API — streams (hierarchical: stream→consumer); create/delete stream (with `--retention`, `--max-msgs`, `--subject`)
- **Pulsar**: Admin REST API (HTTP port 8080, `--admin-port` to override) — topics; create/delete topic (with `--partitions`); `--tenant`/`--namespace`/`--non-persistent` persistent flags with `ResolveTarget`
- **Redis**: `go-redis` commands — queues, topics; purge, stats, create/delete queue, create/delete topic; `--prefix`/`--maxlen` persistent flags with `ResolveTarget`
- **Google Pub/Sub**: Pub/Sub Admin API — topics, subscriptions
- **AWS SQS+SNS**: Native SQS/SNS APIs — queues, topics; native purge, queue stats
- **Azure Service Bus**: Admin API — queues, topics (hierarchical: topic→subscription); queue stats, purge by draining

### Broker-Specific Flags (per-message)

Each broker can register per-message flags via `BrokerSpec.ProduceFlags`/`ConsumeFlags` and read them back via `ProduceExtra`/`ConsumeExtra` into `opts.Extra map[string]string`:
- **Artemis**: `--anycast`/`--multicast` (routing-type override); `--broker-name` (Jolokia management)
- **Kafka**: `--partition`/`--offset` (consume: single-partition reads); key-aware balancer (Hash when key present)
- **MQTT**: `--qos 0|1|2`/`--retain` (produce); `--qos` (consume); `--group` (shared subscription prefix)
- **AWS**: `--fifo`/`--message-group-id`/`--dedup-id` (produce); `--visibility-timeout` (consume)
- **Azure**: `--subscription` (consume: named subscription override)
- **Google**: `--subscription` (consume: named subscription override)
- **NATS**: `--stream` (JetStream stream name override, produce+consume)

### Addressing Resolvers

Brokers with address conventions use `BrokerSpec.ResolveTarget` to map bare names to full addresses:
- **RabbitMQ**: AMQP 1.0 v2 addresses (`/queues/<to>`, `/exchanges/amq.topic/<to>`); `-e`/`-q` flags via `ExchangeRouting: true`
- **Pulsar**: `persistent://<tenant>/<namespace>/<to>` (with `--tenant`/`--namespace`/`--non-persistent`)
- **Redis**: `<prefix>:queue:<to>` / `<prefix>:topic:<to>` (with `--prefix`); full keys passthrough on `:`

## Key Design Decisions

1. **Build-time selection**: Only one broker backend per binary reduces dependencies and binary size
2. **Unified CLI**: Same command structure regardless of broker backend
3. **Standard streams**: Uses stdin for input, stdout for data, stderr for metadata
4. **Auto-topology**: Artemis creates queues/topics on-the-fly; other brokers may require pre-declaration
5. **ANYCAST vs MULTICAST**: Artemis uses message metadata for routing (ANYCAST=queue, MULTICAST=topic)
6. **Lazy adapter creation**: Adapters connect only when a command runs, not at startup
7. **Shared CLI skeleton**: `cmd.BrokerSpec` + `cmd.NewRootCommand()` absorb the boilerplate; each entry file only declares broker-specific flags and adapter factories
8. **Standardised manage output**: `cmd.NewManageCommand()` provides consistent formatting across all brokers

## Module Structure

- `cmd/` - Generic command implementations using backend interfaces
  - `root.go` - `BrokerSpec` + `NewRootCommand` — shared CLI skeleton for all broker entry files
  - `manage.go` - `ManageSpec` + `NewManageCommand` — shared management subcommand builder
  - `command.go` - `WrapQueueCommand`/`WrapTopicCommand` adapter factories
  - `produce.go` - shared producer logic (flags, lines, NDJSON, count, rate) for send/publish
  - `flags.go` - shared flag helpers: dual number/duration time flags (`--timeout`, `--interval`, `--ttl`), kebab-case aliases (`--content-type`, etc.)
  - `send.go`, `receive.go`, `peek.go` - Queue commands
  - `request.go` - Request-reply command (send + wait for reply)
  - `reply.go` - Request-reply responder (consume requests, reply to each reply-to)
  - `move.go` - Move/redrive messages between queues on the same broker
  - `forward.go` - Continuous streaming relay between queues/topics (optional `-x`/`--command` shell command)
  - `bridge.go` - Cross-broker relay via subprocess NDJSON streaming (`--to '<target command>'`)
  - `publish.go`, `subscribe.go` - Topic commands
  - `format.go` - `-F`/`--format` output templating shared by the read commands
  - `signal.go` - interrupt-aware context for long-running commands (reply, move, ping)
  - `stream.go` - streaming infra: timed/interruptible context (`--for`), throughput stats (`--stats`)
  - `ndjson.go` - lossless NDJSON record schema + `--ndjson` export/import helpers
  - `rate.go` - `--rate` producer throughput limiter
  - `ping.go` - broker connectivity health-check command (all brokers)
  - `shell.go` - interactive REPL with persistent connection, readline, Shift+TAB AI shell toggle
  - `pipeline.go` - pipeline parser/executor: split, classify, coalesce, wire, execute; alias expansion (`expandAlias`)
  - `reconnect.go` - `reconnectingQueue`/`reconnectingTopic` with exponential backoff
  - `ai.go` - AI shell session: spinner, API call, confirm-to-run, auto-fix on error (up to `maxFixAttempts`)
  - `aiclient.go` - `aiClient` interface + Anthropic/OpenAI-compat/Gemini implementations
  - `aiconfig.go` - YAML config loading (`xmcConfig` with `aliases` map), AI provider resolution
  - `aiprompt.go` - system prompt from cobra tree, command extraction
  - `aipaths.go` - `~/.xmc/` dir paths, per-binary file naming
- `broker/` - Broker abstraction layer and implementations
  - `amqpcommon/` - Shared AMQP 1.0 code (Artemis + RabbitMQ)
  - `artemis/` - Apache Artemis (AMQP 1.0) + Jolokia management
  - `kafka/` - Apache Kafka + admin client management
  - `ibmmq/` - IBM MQ
  - `mqtt/` - MQTT (paho.mqtt.golang; queue via shared subscriptions, topic pub/sub)
  - `nats/` - NATS / JetStream (JetStream WorkQueue for queues, core NATS for topics)
  - `pulsar/` - Apache Pulsar (persistent:// topics; Shared subscription for queues, Exclusive/Shared for topics)
  - `rabbitmq/` - RabbitMQ (AMQP 1.0) + Management API
  - `redis/` - Redis (Streams for queues and topics; consumer groups for competing consumers)
  - `gcppubsub/` - Google Cloud Pub/Sub (topics + subscriptions; shared subs for queues)
  - `awssqs/` - AWS SQS (queues) + SNS (topics via SNS→SQS fan-out)
  - `azuresb/` - Azure Service Bus (native queues + topics/subscriptions; native peek + TTL)
  - `backends/` - Common queue/topic interfaces, types, and shared helpers (naming, properties, timeout, errors, `ObjectNode`/`Metric` for generic broker objects); `SendOptions`/`PublishOptions`/`ReceiveOptions`/`SubscribeOptions` each have an `Extra map[string]string` for broker-specific flag values
  - `tlsutil/` - Shared TLS configuration builder (used by non-cloud brokers)
- `log/` - Logging utilities with verbose mode support
- `rc/` - Return code constants
- `test/` - bats integration test files

See `docs/BROKERS.md` for protocol-specific differences and topology patterns.
