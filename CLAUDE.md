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

Entry point files (`broker/artemis.go`, etc.) fill in a `cmd.BrokerSpec` struct and call `cmd.NewRootCommand(spec)` which wires up all commands, flags, and the verbose toggle. Management subcommands are built via `cmd.NewManageCommand(spec)` with a `cmd.ManageSpec` declaring the broker's capabilities.

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
- `cmd/root.go` - `BrokerSpec` + `NewRootCommand()` (shared CLI skeleton for all entry files)
- `cmd/manage.go` - `ManageSpec` + `ManageAction` + `BindAction` + `NewManageCommand()` (shared manage subcommand builder with standardised output format; supports list/purge/stats and create/delete for queues, topics, exchanges, and bindings)
- `cmd/produce.go` - `registerProduceFlags`, `parseProduceFlags`, `runProduce` (shared producer logic for send/publish)
- `cmd/command.go` - `WrapQueueCommand`/`WrapTopicCommand` adapter factories with lazy connection

### Command Structure

Uses `spf13/cobra` for CLI:
- Root command provides persistent flags (`--server`, `--user`, `--password`, `--verbose`, TLS flags)
- Environment variables prefixed per flavor: `AMC_` (Artemis), `IMC_` (IBM MQ), `KMC_` (Kafka), `MMC_` (MQTT), `NMC_` (NATS), `PMC_` (Pulsar), `RMC_` (RabbitMQ), `REDMC_` (Redis), `GMC_` (Google Pub/Sub), `AWSMC_` (AWS SQS+SNS), `AZMC_` (Azure Service Bus)
- Queue commands: `send`, `receive`, `peek`, `request`, `reply`, `move`, `forward`
- Topic commands: `publish`, `subscribe` (Kafka also has topic `forward`)
- Connectivity: `ping` (all brokers; connects and reports reachability)
- Management commands: `manage list`, `manage purge`, `manage stats`, `manage create-queue`, `manage delete-queue`, `manage create-topic`, `manage delete-topic`, `manage create-exchange`, `manage delete-exchange`, `manage bind-queue`, `manage unbind-queue`
- Output: `-J` JSON, `-F`/`--format` template, or `--ndjson` lossless records, shared across read commands
- Bulk/load: `-l`/`--lines`, `--ndjson` (input), `-n`/`--count` repeat, `--rate` throttle on send/publish; `-n 0` drains on read commands
- Streaming: `forward` (continuous relay, optional `-x`/`--command` shell command), `--for <duration>` (time-bounded), `--stats` (live throughput) on read commands and `forward`
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

Each broker uses its native management API:
- **Artemis**: Jolokia REST API (HTTP port 8161) — list, purge, stats, create/delete queue, create/delete topic
- **RabbitMQ**: RabbitMQ Management API (HTTP port 15672) — list, purge, stats, create/delete queue, create/delete exchange, bind/unbind queue
- **Kafka**: Admin client via `segmentio/kafka-go` — list, create/delete topic (with `--partitions`, `--replication-factor`, `--config`)
- **IBM MQ**: No management commands (queue management via IBM tooling)
- **NATS**: JetStream API — list streams, create/delete queue (with `--retention`, `--max-msgs`, `--subject`)
- **Pulsar**: Admin REST API (HTTP port 8080, `--admin-port` to override) — list, create/delete topic (with `--partitions`)
- **Redis**: `go-redis` commands — list, purge, stats, create/delete queue, create/delete topic
- **Google Pub/Sub**: Pub/Sub Admin API (list topics/subscriptions only)
- **AWS SQS+SNS**: Native SQS/SNS APIs (list, native purge, queue stats)
- **Azure Service Bus**: Admin API (list queues/topics, queue stats, purge by draining)

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
  - `publish.go`, `subscribe.go` - Topic commands
  - `format.go` - `-F`/`--format` output templating shared by the read commands
  - `signal.go` - interrupt-aware context for long-running commands (reply, move, ping)
  - `stream.go` - streaming infra: timed/interruptible context (`--for`), throughput stats (`--stats`)
  - `ndjson.go` - lossless NDJSON record schema + `--ndjson` export/import helpers
  - `rate.go` - `--rate` producer throughput limiter
  - `ping.go` - broker connectivity health-check command (all brokers)
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
  - `backends/` - Common queue/topic interfaces, types, and shared helpers (naming, properties, timeout, errors)
  - `tlsutil/` - Shared TLS configuration builder (used by non-cloud brokers)
- `log/` - Logging utilities with verbose mode support
- `rc/` - Return code constants
- `test/` - bats integration test files

See `docs/BROKERS.md` for protocol-specific differences and topology patterns.
