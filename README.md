# XMC - Xenomorphic Message Client

[![Build Status](https://github.com/makibytes/xmc/actions/workflows/main_build_and_test_linux.yml/badge.svg)](https://github.com/makibytes/xmc/actions)
[![Go Report Card](https://goreportcard.com/badge/github.com/makibytes/xmc)](https://goreportcard.com/report/github.com/makibytes/xmc)
[![GoDoc](https://godoc.org/github.com/makibytes/xmc?status.svg)](https://godoc.org/github.com/makibytes/xmc)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](https://github.com/makibytes/xmc/blob/main/LICENSE)

This project provides a unified command-line interface (CLI) for sending and receiving messages to/from different Message and Streaming Brokers. The broker backend is selected at **build time** using Go build tags. Each flavor produces its own binary with its own name and environment variable prefix.

## Building for Different Brokers

| Build Command | Binary | Broker | Protocol | Env Prefix |
| --- | --- | --- | --- | --- |
| `go build -tags artemis -o amc .` | `amc` | Apache Artemis | AMQP 1.0 | `AMC_` |
| `./scripts/build-imc-in-container.sh` | `imc` | IBM MQ | IBM MQ | `IMC_` |
| `go build -tags kafka -o kmc .` | `kmc` | Apache Kafka | Kafka | `KMC_` |
| `go build -tags mqtt -o mmc .` | `mmc` | MQTT Brokers | MQTT 3.1.1/5.0 | `MMC_` |
| `go build -tags nats -o nmc .` | `nmc` | NATS | NATS / JetStream | `NMC_` |
| `go build -tags pulsar -o pmc .` | `pmc` | Apache Pulsar | Pulsar native | `PMC_` |
| `go build -tags rabbitmq -o rmc .` | `rmc` | RabbitMQ v4+ | AMQP 1.0 | `RMC_` |

Build all flavors for the platform matrix (`linux/amd64`, `linux/arm64`, `darwin/arm64`, `windows/amd64`):

```sh
./scripts/build-platform-matrix.sh
```

Artifacts are written under `dist/<goos>-<goarch>/`.

The goal of this project is to support a common set of features across the different
protocols and brokers, comparable to the JMS API. See broker/BROKERS.md for more details.

## Usage

After building for your chosen broker, the binary provides the same interface regardless of backend. In the examples below, `xmc` is used as a placeholder — substitute it with the actual binary name (`amc`, `imc`, `kmc`, `mmc`, `nmc`, `pmc`, or `rmc`).

### Connection Parameters

The following parameters and environment variables can be used for all commands:

```sh
  -s, --server string      server URL of the broker    [$<PREFIX>_SERVER]
  -u, --user string        username for SASL login     [$<PREFIX>_USER]
  -p, --password string    password for SASL login     [$<PREFIX>_PASSWORD]
  -v, --verbose            print verbose output
```

Environment variables are prefixed per flavor: `AMC_` for Artemis, `IMC_` for IBM MQ, `KMC_` for Kafka, `MMC_` for MQTT, `NMC_` for NATS, `PMC_` for Pulsar, `RMC_` for RabbitMQ.

### TLS / SSL

All brokers support TLS connections:

```sh
  --tls                    enable TLS connection
  --ca-cert string         path to CA certificate file
  --cert string            path to client certificate file
  --key-file string        path to client private key file
  --insecure               skip TLS certificate verification
```

TLS is auto-detected when using `amqps://` or `kafka+ssl://` URL schemes.

### Queue Commands

#### send

Send a message to a queue:

```sh
xmc send <queue> <message>
xmc send <queue> < message.dat           # read from stdin
echo -e "line1\nline2" | xmc send -l <queue>  # send each line as separate message
```

Flags:

```
  -T, --contenttype string     MIME type (default "text/plain")
  -C, --correlationid string   correlation ID
  -I, --messageid string       message ID
  -Y, --priority int           priority 0-9 (default 4)
  -d, --persistent             make message persistent
  -R, --replyto string         reply-to queue
  -P, --property strings       properties in key=value format
  -n, --count int              send the message N times (default 1)
  -E, --ttl int                time-to-live in milliseconds (0 = no expiry)
  -l, --lines                  read stdin line by line, send each as separate message
```

#### receive

Receive (destructive read) a message from a queue:

```sh
xmc receive <queue>
xmc receive -w <queue>             # wait for a message
xmc receive -n 10 <queue>          # receive 10 messages
xmc receive -J <queue>             # output as JSON
xmc receive -S "color='red'" <queue>  # filter by selector
```

Flags:

```
  -t, --timeout float32    seconds to wait (default 0.1)
  -w, --wait               wait endlessly for a message
  -q, --quiet              show data only, suppress properties
  -n, --count int          number of messages to receive (default 1)
  -J, --json               output as JSON
  -S, --selector string    JMS-style message selector expression
```

#### peek

Peek at a message without removing it (non-destructive read):

```sh
xmc peek <queue>
xmc peek -n 5 -J <queue>          # peek 5 messages as JSON
```

Same flags as `receive` except messages are never consumed.

#### request

Send a message and wait for a reply (request-reply pattern):

```sh
xmc request <queue> <message>
xmc request -R my-reply-queue <queue> <message>
xmc request -J <queue> <message>   # output reply as JSON
```

Flags:

```
  -R, --replyto string     reply queue (default "xmc.reply")
  -t, --timeout float32    seconds to wait for reply (default 30)
  -J, --json               output reply as JSON
  -q, --quiet              show data only
```

Plus all `send` flags for the outgoing message.

### Topic Commands

#### publish

Publish a message to a topic:

```sh
xmc publish <topic> <message>
xmc publish -n 100 <topic> <message>   # publish 100 times
```

Same flags as `send`, plus:

```
  -K, --key string         message key for partitioning (Kafka)
```

#### subscribe

Subscribe and receive a message from a topic:

```sh
xmc subscribe <topic>
xmc subscribe -n 10 -J <topic>        # receive 10 as JSON
xmc subscribe -D <topic>              # durable subscription
xmc subscribe -S "type='order'" <topic>  # with selector
```

Same flags as `receive`, plus:

```
  -g, --group string       consumer group ID (default "xmc-consumer-group")
  -D, --durable            create a durable subscription
```

### Management Commands

Broker management operations (available for Artemis, RabbitMQ, Kafka):

```sh
xmc manage list                    # list queues/topics
xmc manage purge <queue>           # remove all messages from a queue
xmc manage stats <queue>           # show queue statistics
```

### Application Properties

You can set properties (metadata) for the message:

```sh
xmc send <queue> -P key1=value1 -P key2=value2 <message>
```

If a message has properties, `receive` shows them automatically. Use `-q` to suppress.

### Working with Files and Redirection

The message can be read from file:

```sh
xmc send <queue> < message.dat
```

By redirecting the output of `receive`, the message data (and only the data) will
be written to a file:

```sh
xmc receive <queue> > message.dat
```

The file will be exactly the same as it was sent. Without redirection the binary
adds a newline character at the end of the message data for better readability.

### JSON Output

Use `-J` to get structured JSON output from receive, peek, subscribe, and request:

```sh
$ xmc receive -J test-queue
{"data":"hello world","messageId":"ID:123","properties":{"env":"prod"}}
```

### Version

Print the version of the binary:

```sh
xmc version
```

## Testing

Unit tests (no broker required):

```sh
go test ./cmd/ ./log/ ./broker/amqpcommon/
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

## Contributing

Contributions are welcome. Please open an issue or submit a pull request.
Use the latest version of Go and run tests with Artemis.

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details
