# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

AMC is a unified command-line interface for sending and receiving messages to/from different Message and Streaming Brokers. The broker backend is selected at **build time** using Go build tags (`artemis`, `ibmmq`, `kafka`, `mqtt`, `rabbitmq`). The goal is to support a common set of features across different protocols and brokers, comparable to the JMS API.

## Building

Build for a specific broker using build tags:

```bash
go build -tags artemis        # Apache Artemis (AMQP 1.0)
go build -tags ibmmq          # IBM MQ
go build -tags kafka          # Apache Kafka
go build -tags mqtt           # MQTT Brokers
go build -tags rabbitmq       # RabbitMQ (AMQP 1.0 - v4+)
```

Default build (without tags) will fail with "No broker loaded" error at runtime.

## Testing

Tests use the bats testing framework (included in `test/bats/`) and require a local Artemis broker:

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

### Broker Interface Pattern

Each broker implements similar patterns:
- `NewBroker()` factory function
- `RootCommand()` returns configured cobra command tree
- Command implementations (`put`, `get`, `peek`) with broker-specific logic
- Connection handling and message translation to/from broker-native formats

### Common Abstractions

Shared types in `broker/`:
- `send_args.go` - Common message sending parameters
- `receive_args.go` - Common message receiving parameters
- Backend interfaces in `broker/backends/` for queue and topic operations

### Command Structure

Uses `spf13/cobra` for CLI:
- Root command provides persistent flags (`--server`, `--user`, `--password`)
- Environment variables prefixed with `AMC_` (e.g., `AMC_SERVER`, `AMC_USER`, `AMC_PASSWORD`)
- Three main commands: `put`, `get`, `peek`
- Connection parameters apply globally across all commands

### Message Handling

- **Application Properties**: Key-value metadata set with `-P key=value`
- **MessageProperties**: AMQP protocol-level properties (message-id, user-id, etc.), shown in verbose mode only
- **Data**: Message payload, can be passed as argument or via stdin
- Output to stdout omits newline when redirected for binary data preservation

## Key Design Decisions

1. **Build-time selection**: Only one broker backend per binary reduces dependencies and binary size
2. **Unified CLI**: Same command structure regardless of broker backend
3. **Standard streams**: Uses stdin for input, stdout for data, stderr for metadata
4. **Auto-topology**: Artemis creates queues/topics on-the-fly; other brokers may require pre-declaration
5. **ANYCAST vs MULTICAST**: Artemis uses message metadata for routing (ANYCAST=queue, MULTICAST=topic)

## Module Structure

- `cmd/` - Legacy command implementations (being replaced by broker-specific implementations)
- `broker/` - Broker abstraction layer and implementations
  - `artemis/` - Apache Artemis (AMQP 1.0)
  - `kafka/` - Apache Kafka
  - `ibmmq/` - IBM MQ
  - `mqtt/` - MQTT
  - `rabbitmq/` - RabbitMQ (AMQP 1.0)
  - `backends/` - Common queue/topic abstractions
- `log/` - Logging utilities with verbose mode support
- `rc/` - Runtime configuration
- `test/` - bats test files

See `broker/BROKERS.md` for protocol-specific differences and topology patterns.