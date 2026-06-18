# xmc - HowTo Build the Binary for Your Broker

| Build Command | Binary | Broker | Protocol | Env Prefix |
| --- | --- | --- | --- | --- |
| `go build -tags artemis -o amc .` | `amc` | Apache Artemis | AMQP 1.0 | `AMC_` |
| `go build -tags aws -o awsmc .` | `awsmc` | AWS SQS + SNS | AWS SDK (HTTPS) | `AWSMC_` |
| `go build -tags azure -o azmc .` | `azmc` | Azure Service Bus | AMQP 1.0 (Azure SDK) | `AZMC_` |
| `go build -tags google -o gmc .` | `gmc` | Google Cloud Pub/Sub | gRPC | `GMC_` |
| `./scripts/build-imc-in-container.sh` | `imc` | IBM MQ | IBM MQ | `IMC_` |
| `go build -tags kafka -o kmc .` | `kmc` | Apache Kafka | Kafka | `KMC_` |
| `go build -tags mqtt -o mmc .` | `mmc` | MQTT Brokers | MQTT 3.1.1 | `MMC_` |
| `go build -tags nats -o nmc .` | `nmc` | NATS | NATS / JetStream | `NMC_` |
| `go build -tags pulsar -o pmc .` | `pmc` | Apache Pulsar | Pulsar native | `PMC_` |
| `go build -tags rabbitmq -o rmc .` | `rmc` | RabbitMQ v4+ | AMQP 1.0 | `RMC_` |
| `go build -tags redis -o redmc .` | `redmc` | Redis | Redis Streams | `REDMC_` |

Build all flavors for the platform matrix (`linux/amd64`, `linux/arm64`, `darwin/arm64`, `windows/amd64`):

```sh
./scripts/build-platform-matrix.sh
```

Artifacts are written under `dist/<goos>-<goarch>/`.

## Building

Build a binary for a specific broker:

```bash
go build -tags <tag> -o <binary> .
```

For example: `go build -tags rabbitmq -o rmc .`

See the main [README](../README.md) for the full build table and platform matrix script.

## Testing

Unit tests (no broker required):

```sh
go test ./cmd/ ./log/ ./broker/backends/ ./broker/amqpcommon/ ./broker/tlsutil/
```

Integration tests are based on the [bats testing framework](https://github.com/bats-core/bats-core)
(included) and depend on a local Artemis broker with its default settings.

If you have Docker you can spin up an Artemis container like so:

```sh
docker run --name artemis -d \
    -p 8161:8161 -p 5672:5672 \
    apache/activemq-artemis:latest-alpine
```

Port 5672 is the default port of the AMQP 1.0 protocol. Port 8161 provides access to the Artemis web console,
where you can check the queues and messages manually. Default credentials are artemis/artemis.

Then you can start the tests:

```sh
./run-tests.sh
```

### Integration Tests (testcontainers)

Integration tests start real broker containers automatically via [testcontainers-go](https://golang.testcontainers.org/).
Requires Docker.

Run all broker integration tests:

```sh
./run-integration-tests.sh
```

Run a single broker:

```sh
go test -tags "artemis integration" -timeout 120s -v ./broker/artemis/
```

Integration tests are tagged with both the broker build tag and `integration`:

```go
//go:build artemis && integration
```

IBM MQ is built in a Docker container that downloads the IBM MQ Redistributable SDK and compiles `imc` without requiring local MQ headers.
This requires a working Docker installation.

If you prefer building IBM MQ locally (with IBM MQ SDK already installed), you can still use:

```sh
go build -tags ibmmq -o imc .
```

For platform matrix builds, IBM MQ has these constraints:

- `linux/amd64`: built in Docker via `scripts/build-imc-in-container.sh`.
- `linux/arm64`: requires native Linux ARM64 runner with IBM MQ SDK/client installed (public redistributable feed currently provides Linux X64 package).
- `darwin/arm64`: requires native macOS arm64 build host with IBM MQ Dev Toolkit installed.
- `windows/amd64`: requires native Windows x64 build host with IBM MQ SDK installed.

## Other Documentation

- [MCP Server](MCP.md) — Model Context Protocol server for AI-assisted messaging
- [Broker Differences](BROKERS.md) — Feature matrix and protocol-specific details
