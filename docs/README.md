# xmc Getting Started Guides

Getting started guides for each supported broker. Each guide covers authentication, basic send/receive, admin commands, advanced xmc features, and cross-broker bridging.

| Guide | Binary | Broker | Protocol |
| --- | --- | --- | --- |
| [artemis.md](artemis.md) | `amc` | Apache Artemis | AMQP 1.0 |
| [aws.md](aws.md) | `awsmc` | AWS SQS + SNS | AWS SDK (HTTPS) |
| [azure.md](azure.md) | `azmc` | Azure Service Bus | AMQP 1.0 (Azure SDK) |
| [google.md](google.md) | `gmc` | Google Cloud Pub/Sub | gRPC |
| [ibmmq.md](ibmmq.md) | `imc` | IBM MQ | IBM MQ |
| [kafka.md](kafka.md) | `kmc` | Apache Kafka | Kafka |
| [mqtt.md](mqtt.md) | `mmc` | MQTT Brokers | MQTT 3.1.1 |
| [nats.md](nats.md) | `nmc` | NATS / JetStream | NATS |
| [pulsar.md](pulsar.md) | `pmc` | Apache Pulsar | Pulsar native |
| [rabbitmq.md](rabbitmq.md) | `rmc` | RabbitMQ v4+ | AMQP 1.0 |
| [redis.md](redis.md) | `redmc` | Redis | Redis Streams |

## Building

Build a binary for a specific broker:

```bash
go build -tags <tag> -o <binary> .
```

For example: `go build -tags rabbitmq -o rmc .`

See the main [README](../README.md) for the full build table and platform matrix script.

## Other Documentation

- [Bridging Brokers](bridging.md) — Pipe messages between brokers without data loss using `--ndjson`
- [MCP Server](MCP.md) — Model Context Protocol server for AI-assisted messaging
- [Broker Differences](../broker/BROKERS.md) — Feature matrix and protocol-specific details
