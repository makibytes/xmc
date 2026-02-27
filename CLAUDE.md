# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

XMC (Xenomorphic Message Client) is a unified command-line interface for sending and receiving messages to/from different Message and Streaming Brokers. The broker backend is selected at **build time** using Go build tags (`artemis`, `ibmmq`, `kafka`, `mqtt`, `nats`, `rabbitmq`). Each flavor produces its own binary (`amc`, `imc`, `kmc`, `mmc`, `nmc`, `rmc`) with its own environment variable prefix. The goal is to support a common set of features across different protocols and brokers, comparable to the JMS API.

## Building

Build for a specific broker using build tags:

```bash
go build -tags artemis -o amc .    # Apache Artemis (AMQP 1.0)
go build -tags ibmmq -o imc .     # IBM MQ
go build -tags kafka -o kmc .     # Apache Kafka
go build -tags mqtt -o mmc .      # MQTT Brokers
go build -tags nats -o nmc .      # NATS / JetStream
go build -tags rabbitmq -o rmc .  # RabbitMQ v4+ (AMQP 1.0)
```

Default build (without tags) will fail with "No broker loaded" error at runtime.

## Testing

Unit tests (no broker required):

```bash
go test ./cmd/ ./log/ ./broker/amqpcommon/
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

### Adapter Pattern

Each broker provides adapters implementing the backend interfaces:

- `QueueBackend` interface (`broker/backends/queue.go`) - for queue operations (send, receive, peek, request)
- `TopicBackend` interface (`broker/backends/topic.go`) - for topic operations (publish, subscribe)
- `ManageableBackend` interface (`broker/backends/queue.go`) - optional, for management operations (list, purge, stats)
- `ManageableTopicBackend` interface (`broker/backends/topic.go`) - optional, for topic management (list topics)

Entry point files (`broker/artemis.go`, etc.) use `cmd.WrapQueueCommand` / `cmd.WrapTopicCommand` helpers to lazily create adapters at command execution time.

### Shared AMQP Code

Artemis and RabbitMQ both use AMQP 1.0. Shared logic is in `broker/amqpcommon/`:
- `connect.go` - AMQP connection with SASL authentication and TLS support
- `receive.go` - Message receive with timeout, acknowledge/release, selectors, and durable subscriptions
- `message.go` - AMQP message to backend Message conversion

Broker-specific differences (Artemis routing annotations, RabbitMQ exchange routing) remain in their respective packages.

### Command Structure

Uses `spf13/cobra` for CLI:
- Root command provides persistent flags (`--server`, `--user`, `--password`, `--verbose`, TLS flags)
- Environment variables prefixed per flavor: `AMC_` (Artemis), `IMC_` (IBM MQ), `KMC_` (Kafka), `MMC_` (MQTT), `NMC_` (NATS), `RMC_` (RabbitMQ)
- Queue commands: `send`, `receive`, `peek`, `request`
- Topic commands: `publish`, `subscribe`
- Management commands: `manage list`, `manage purge`, `manage stats`
- Connection parameters apply globally across all commands

### Message Handling

- **Application Properties**: Key-value metadata set with `-P key=value`
- **MessageProperties**: AMQP protocol-level properties (message-id, user-id, etc.), shown in verbose mode only
- **Data**: Message payload, can be passed as argument or via stdin
- Output to stdout omits newline when redirected for binary data preservation
- **JSON output** (`-J`): Structured JSON output for receive, peek, subscribe, and request commands
- **Line-delimited mode** (`-l`): Read stdin line by line, send/publish each line as separate message

### TLS Support

All brokers support TLS via persistent flags (`--tls`, `--ca-cert`, `--cert`, `--key-file`, `--insecure`).
- AMQP brokers (Artemis, RabbitMQ): auto-detect `amqps://` URL scheme
- Kafka: auto-detect `kafka+ssl://` URL scheme
- MQTT: auto-detect `ssl://` URL scheme
- Shared TLS config in `broker/amqpcommon/connect.go` for AMQP brokers
- Kafka has its own TLS config in `broker/kafka/connect.go`

### Management APIs

Each broker uses its native management API:
- **Artemis**: Jolokia REST API (HTTP port 8161)
- **RabbitMQ**: RabbitMQ Management API (HTTP port 15672)
- **Kafka**: Admin client via `segmentio/kafka-go` (topic listing only)
- **IBM MQ**: No management commands (queue management via IBM tooling)
- **NATS**: JetStream API (stream listing = queue listing)

## Key Design Decisions

1. **Build-time selection**: Only one broker backend per binary reduces dependencies and binary size
2. **Unified CLI**: Same command structure regardless of broker backend
3. **Standard streams**: Uses stdin for input, stdout for data, stderr for metadata
4. **Auto-topology**: Artemis creates queues/topics on-the-fly; other brokers may require pre-declaration
5. **ANYCAST vs MULTICAST**: Artemis uses message metadata for routing (ANYCAST=queue, MULTICAST=topic)
6. **Lazy adapter creation**: Adapters connect only when a command runs, not at startup

## Module Structure

- `cmd/` - Generic command implementations using backend interfaces
  - `command.go` - `WrapQueueCommand`/`WrapTopicCommand` adapter factories
  - `send.go`, `receive.go`, `peek.go` - Queue commands
  - `request.go` - Request-reply command (send + wait for reply)
  - `publish.go`, `subscribe.go` - Topic commands
- `broker/` - Broker abstraction layer and implementations
  - `amqpcommon/` - Shared AMQP 1.0 code (Artemis + RabbitMQ)
  - `artemis/` - Apache Artemis (AMQP 1.0) + Jolokia management
  - `kafka/` - Apache Kafka + admin client management
  - `ibmmq/` - IBM MQ
  - `mqtt/` - MQTT (paho.mqtt.golang; queue via shared subscriptions, topic pub/sub)
  - `nats/` - NATS / JetStream (JetStream WorkQueue for queues, core NATS for topics)
  - `rabbitmq/` - RabbitMQ (AMQP 1.0) + Management API
  - `backends/` - Common queue/topic interfaces and types
- `log/` - Logging utilities with verbose mode support
- `rc/` - Return code constants
- `test/` - bats integration test files

See `broker/BROKERS.md` for protocol-specific differences and topology patterns.
