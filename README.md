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

The goal of this project is to support a common set of features across the different
protocols and brokers, comparable to the JMS API. See broker/BROKERS.md for more details.

## Usage

After building for your chosen broker, the binary provides the same interface regardless of backend. In the examples below, `xmc` is used as a placeholder — substitute it with the actual binary name (`amc`, `awsmc`, `azmc`, `gmc`, `imc`, `kmc`, `mmc`, `nmc`, `pmc`, `rmc`, or `redmc`).

### Connection Parameters

Most brokers use a server URL with optional credentials:

```sh
  -s, --server string      server URL of the broker    [$<PREFIX>_SERVER]
  -u, --user string        username for SASL login     [$<PREFIX>_USER]
  -p, --password string    password for SASL login     [$<PREFIX>_PASSWORD]
  -v, --verbose            print verbose output
```

Environment variables are prefixed per flavor: `AMC_` for Artemis, `IMC_` for IBM MQ, `KMC_` for Kafka, `MMC_` for MQTT, `NMC_` for NATS, `PMC_` for Pulsar, `RMC_` for RabbitMQ, `REDMC_` for Redis.

Cloud brokers use different connection parameters:

| Broker | Primary flag | Authentication |
| --- | --- | --- |
| Google Pub/Sub (`gmc`) | `-s` / `--project` | Application Default Credentials or `--credentials` (service account JSON) |
| AWS SQS+SNS (`awsmc`) | `-s` / `--region` | AWS credential chain (env / shared config / IAM); `--profile`, `--endpoint` (for LocalStack) |
| Azure Service Bus (`azmc`) | `-s` / `--connection-string` | SAS connection string or `--namespace` (Azure AD via `DefaultAzureCredential`) |

### TLS / SSL

Non-cloud brokers support TLS connections:

```sh
  --tls                    enable TLS connection
  --ca-cert string         path to CA certificate file
  --cert string            path to client certificate file
  --key-file string        path to client private key file
  --insecure               skip TLS certificate verification
```

TLS is auto-detected when using `amqps://`, `kafka+ssl://`, `ssl://`, `pulsar+ssl://`, or `rediss://` URL schemes. Cloud brokers (Google, AWS, Azure) and IBM MQ handle TLS internally.

### Queue Commands

#### send

Send a message to a queue:

```sh
xmc send <queue> <message>
xmc send <queue> < message.dat           # read from stdin
echo -e "line1\nline2" | xmc send -l <queue>  # send each line as separate message
xmc send --ndjson <queue> < backup.ndjson     # restore messages with full metadata
xmc send -n 100000 --rate 5000 <queue> hi     # load test: 100k messages at 5000/s
```

Flags:

```
  -T, --content-type string    MIME type (default "text/plain")
  -C, --correlation-id string  correlation ID
  -I, --message-id string      message ID
  -Y, --priority int           priority 0-9 (default 4)
  -d, --persistent             make message persistent
  -R, --reply-to string        reply-to queue
  -P, --property strings       properties in key=value format
  -n, --count int              send the message N times (default 1)
  -E, --ttl duration           time-to-live, e.g. "5s" (0 = no expiry)
  -l, --lines                  read stdin line by line, send each as separate message
      --ndjson                 read NDJSON records from stdin, send each (lossless import)
      --rate float             throttle to at most N messages/second (0 = unlimited)

Legacy concatenated names (`--contenttype`, `--correlationid`, `--messageid`,
`--replyto`) are still accepted as aliases. Time flags such as `--ttl` accept a
human-readable duration (`100ms`, `5s`, `1m`) or a plain number (milliseconds for
`--ttl`, seconds for `--timeout`/`--interval`).
```

#### receive

Receive (destructive read) a message from a queue:

```sh
xmc receive <queue>
xmc receive -w <queue>             # wait for a message
xmc receive -n 10 <queue>          # receive 10 messages
xmc receive -J <queue>             # output as JSON
xmc receive -n 0 <queue>           # drain all currently available messages
xmc receive -S "color='red'" <queue>  # filter by selector
```

Flags:

```
  -t, --timeout duration   time to wait, e.g. "100ms" (default 100ms)
  -w, --wait               wait endlessly for a message
  -q, --quiet              show data only, suppress properties
  -n, --count int          number of messages to receive (default 1, 0 = drain all)
  -J, --json               output as JSON
  -F, --format string      render each message with a kcat-style template
      --ndjson             output one lossless JSON record per line (export)
  -S, --selector string    JMS-style message selector expression
      --for duration       stream for a bounded time then stop (e.g. "30s", "5m")
      --stats              print live throughput statistics to stderr while streaming
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
  -R, --reply-to string    reply queue (default "xmc.reply")
  -t, --timeout duration   time to wait for the reply, e.g. "30s" (default 30s)
  -J, --json               output reply as JSON
  -q, --quiet              show data only
```

Plus all `send` flags for the outgoing message.

#### reply

Reply to requests on a queue (the responder side of request-reply):

```sh
xmc reply <queue> <response>            # reply with a fixed payload
xmc reply <queue> --echo                # echo each request back
xmc reply <queue> --command "jq .data"  # pipe each request through a command
xmc reply -n 1 <queue> "pong"           # serve a single request, then exit
```

Flags:

```
  -e, --echo                 echo the request payload back as the response
  -x, --command string       run a shell command per request; its stdout is the reply
  -R, --reply-to string      fallback reply destination if a request carries no reply-to
  -T, --content-type string  MIME type of the response (default "text/plain")
  -P, --property strings     response properties in key=value format
  -n, --count int            number of requests to serve (0 = serve until interrupted)
  -t, --timeout duration     time to wait per request, e.g. "5s" (0 = indefinitely)
  -S, --selector string      only handle requests matching the selector
  -q, --quiet                suppress per-request logging
```

The reply's correlation ID is taken from the request's correlation ID, falling back
to its message ID, so the original `request` caller can match the response. The
responder runs until interrupted (Ctrl-C) unless `--count` is given.

#### move

Move messages from one queue to another on the same broker — typically to redrive
a dead-letter queue back onto its processing queue after fixing the cause of failure:

```sh
xmc move <source> <destination>          # move all available messages
xmc move -n 10 <source> <destination>    # move at most 10
xmc move -S "attempts > 3" dlq orders     # move only matching messages
```

Flags:

```
  -n, --count int          maximum messages to move (0 = all available)
  -S, --selector string    only move messages matching the selector
  -t, --timeout duration   time to wait for the next source message (default 100ms)
  -q, --quiet              print only the final summary
```

The move is destructive: each message is consumed from the source before being sent
to the destination. Message metadata (correlation ID, content type, reply-to,
priority, persistence, and application properties) is preserved; the destination
assigns a fresh message ID. If a send fails, the in-flight message is written to
stdout so it can be recovered, and the command stops.

#### forward

Continuously relay messages from one queue to another on the same broker. Unlike
`move`, which drains what is present and stops, `forward` keeps streaming as new
messages arrive — useful for live bridging, mirroring traffic while debugging, or
continuously redriving a dead-letter queue:

```sh
xmc forward <source> <destination>             # relay until interrupted (Ctrl-C)
xmc forward --for 5m orders orders-backup       # relay for five minutes
xmc forward -n 100 orders orders-backup         # relay 100 messages then stop
xmc forward -x "jq -c ." raw normalized         # transform each message in flight
xmc forward --stats orders mirror               # show live throughput on stderr
```

Flags:

```
  -x, --command string     pipe each message through a shell command; its stdout is forwarded
  -n, --count int          maximum messages to forward (0 = until interrupted)
  -t, --timeout duration   time to wait for the next source message per poll (default 100ms)
      --for duration       relay for a bounded time then stop (e.g. "30s", "5m")
      --stats              print live throughput statistics to stderr
  -S, --selector string    only forward messages matching the selector
  -q, --quiet              print only the final summary
```

Like `move`, the relay is destructive on the source and preserves message
metadata (the destination assigns a fresh message ID). If a transform or send
fails, the consumed message is written to stdout so it can be recovered. On
topic-only brokers (Kafka) `forward` relays between topics instead of queues.

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

Broker management operations (available for most brokers — capabilities vary):

```sh
xmc manage list                    # list queues/topics
xmc manage purge <queue>           # remove all messages from a queue
xmc manage stats <queue>           # show queue statistics
```

| Broker | list | purge | stats |
| --- | --- | --- | --- |
| Artemis | queues | yes | yes |
| AWS SQS+SNS | queues + topics | yes (native) | yes |
| Azure Service Bus | queues + topics | yes (drains) | yes |
| Google Pub/Sub | topics + subscriptions | — | — |
| Kafka | topics | — | — |
| NATS | streams (queues) | — | — |
| Pulsar | topics | — | — |
| RabbitMQ | queues | yes | yes |
| Redis | queues + topics | yes | yes |
| IBM MQ | — | — | — |
| MQTT | — | — | — |

### Application Properties

You can set properties (metadata) for the message:

```sh
xmc send <queue> -P key1=value1 -P key2=value2 <message>
```

If a message has properties, `receive` shows them automatically. Use `-q` to suppress.

**Note:** MQTT 3.1.1 has no user properties at the protocol level, so application properties and metadata (correlation-id, reply-to, content-type, message-id) cannot be carried through an MQTT broker.

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

### Custom Output Format

Use `-F`/`--format` with `receive`, `peek`, `subscribe`, and `request` to render
each message through a template (this overrides `-J`):

```sh
xmc receive -F "%i %s\n" orders
xmc subscribe -F "tenant=%p{tenant} body=%s\n" events
```

Format tokens:

```
  %s        message payload (data)
  %S        payload length in bytes
  %i        message ID
  %c        correlation ID
  %r        reply-to
  %y        content type
  %P        priority
  %u        persistent (true/false)
  %h        all properties as sorted key=value pairs
  %p{key}   value of application property "key"
  %m{key}   value of internal metadata "key"
  %%        a literal percent sign
```

The escapes `\n`, `\t`, `\r`, and `\\` are interpreted in the template, and unknown
tokens are emitted verbatim. Unlike the default output, no trailing newline is added,
so include `\n` where you want one.

### Export and Import (NDJSON)

`--ndjson` provides a lossless, round-trippable representation of messages as
newline-delimited JSON — one record per line. Unlike `-J` (a human-readable
dump), NDJSON is designed to be re-imported: binary payloads are base64-encoded
and all metadata (message ID, correlation ID, reply-to, content type, priority,
persistence, and properties) is preserved.

Export by consuming with `--ndjson`; import by producing with `--ndjson`:

```sh
# Back up an entire queue to a file (drain everything)
xmc receive -n 0 --ndjson orders > orders.ndjson

# Restore it (or load it onto a different broker / queue)
xmc send --ndjson orders < orders.ndjson

# Topics work the same way
xmc subscribe -n 0 --ndjson events > events.ndjson
xmc publish --ndjson events < events.ndjson
```

This makes queue backup/restore, migration between brokers, and offline
inspection or transformation (e.g. with `jq`) straightforward. On the consuming
side `--ndjson` overrides `-F` and `-J`.

### Rate Limiting

`--rate` caps producer throughput (messages per second) on `send` and `publish`.
Combined with `-n` (repeat) or `-l`/`--ndjson` (bulk input), it turns xmc into a
simple, controllable load generator:

```sh
xmc send -n 1000000 --rate 10000 work "payload"   # 1M messages at 10k/s
xmc send --ndjson --rate 500 work < big.ndjson      # replay a capture at 500/s
```

A rate of 0 (the default) means no limit.

### Connectivity (ping)

Check that the broker is reachable, including authentication and TLS handshake:

```sh
xmc ping                  # single connection attempt
xmc ping -n 5             # five attempts, one per second
xmc ping -n 0 -i 2        # keep pinging every 2s until interrupted
```

```
  -n, --count int          number of attempts (0 = until interrupted, default 1)
  -i, --interval duration  time between attempts, e.g. "500ms" (default 1s)
```

`ping` exits non-zero if any attempt fails, which makes it convenient for
readiness checks and CI pipelines. It is available for every broker.

### Streaming

`xmc` can run as a continuous stream processor rather than a one-shot client.
Three composable options turn the read commands (`receive`, `peek`, `subscribe`)
and `forward` into streaming tools:

- **`forward`** — a continuous relay that consumes from a source and produces to
  a destination as messages arrive, optionally transforming each one with `-x`.
- **`--for <duration>`** — bound a stream to a wall-clock window, then stop
  cleanly (e.g. capture 30 seconds of a topic). Accepts Go duration strings such
  as `30s`, `5m`, `1h30m`.
- **`--stats`** — print live throughput (messages, msgs/sec, byte total) to
  stderr while streaming, with a final summary when the stream ends. Because the
  report goes to stderr, stdout stays a clean message stream you can pipe.

They combine naturally:

```sh
xmc subscribe -n 0 --for 30s --stats events       # sample a topic for 30s with metrics
xmc subscribe -n 0 --ndjson events > capture.ndjson # capture a live stream to a file
xmc forward --for 1h --stats orders mirror          # mirror a queue for an hour with metrics
```

A streaming read uses the per-poll `--timeout` to pace itself and stops at the
next message boundary when the window elapses or on Ctrl-C, so `--stats` totals
are always printed. Combined with `-n` (count) you get "whichever comes first":
`xmc receive -n 1000 --for 10s q` stops at 1000 messages or 10 seconds.

### Version

Print the version of the binary:

```sh
xmc version
```

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

## Contributing

Contributions are welcome. Please open an issue or submit a pull request.
Use the latest version of Go and run tests with Artemis.

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details
