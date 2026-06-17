# Differences Between Brokers

## Feature Matrix

| Feature | Artemis | RabbitMQ | Kafka | IBM MQ | MQTT | NATS | Pulsar | Redis | GCP Pub/Sub |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| Queue send/receive/peek | Yes | Yes | - | Yes | Yes | Yes | Yes | Yes | Yes |
| Topic publish/subscribe | Yes | Yes | Yes | - | Yes | Yes | Yes | Yes | Yes |
| Request-reply | Yes | Yes | - | Yes | - | Yes | Yes | Yes | Yes |
| Reply / responder | Yes | Yes | - | Yes | - | Yes | Yes | Yes | Yes |
| Move / redrive | Yes | Yes | - | Yes | Yes | Yes | Yes | Yes | Yes |
| Custom output format (`-F`) | Yes | Yes | Yes | Yes | Yes | Yes | Yes | Yes | Yes |
| NDJSON export/import (`--ndjson`) | Yes | Yes | Yes | Yes | Yes | Yes | Yes | Yes | Yes |
| Drain all (`-n 0`) | Yes | Yes | Yes | Yes | Yes | Yes | Yes | Yes | Yes |
| Producer rate limit (`--rate`) | Yes | Yes | Yes | Yes | Yes | Yes | Yes | Yes | Yes |
| Connectivity check (`ping`) | Yes | Yes | Yes | Yes | Yes | Yes | Yes | Yes | Yes |
| Streaming relay (`forward`) | Yes | Yes | Yes | Yes | Yes | Yes | Yes | Yes | Yes |
| Time-bounded streaming (`--for`) | Yes | Yes | Yes | Yes | Yes | Yes | Yes | Yes | Yes |
| Live throughput (`--stats`) | Yes | Yes | Yes | Yes | Yes | Yes | Yes | Yes | Yes |
| TLS / SSL | Yes | Yes | Yes | - | Yes | Yes | Yes | Yes | - |
| Message selectors | Yes | Yes | - | Yes | - | - | - | - | - |
| Durable subscriptions | Yes | Yes | - | - | - | - | Yes | Yes | Yes |
| TTL / expiry | Yes | Yes | Yes | Yes | - | - | Partial | - | - |
| Application properties | Yes | Yes | Yes | Yes | - | - | Yes | Yes | Yes |
| Message priority | Yes | Yes | - | Yes | - | - | - | - | - |
| Persistent delivery | Yes | Yes | - | Yes | Yes (QoS 1) | Yes (JetStream) | Yes (persistent://) | Yes (Streams) | Yes |
| Management: list | Yes | Yes | Yes | - | - | Yes | Yes | Yes | Yes |
| Management: purge | Yes | Yes | - | - | - | - | - | Yes | - |
| Management: stats | Yes | Yes | - | - | - | - | - | Yes | - |

The `reply`, `move` and `-F`/`--format` features live in the generic command layer
(`cmd/`) on top of the queue/topic interfaces, so they are available for every broker
that exposes the underlying operation. `reply` and `move` require queue support (hence
unavailable for Kafka), and end-to-end `reply` additionally depends on the broker
conveying a reply-to address (use `--replyto` as a fallback where it does not).
`-F`/`--format` is purely client-side rendering and works for every broker's read commands.

Likewise, `--ndjson` (lossless export/import), `-n 0` (drain), `--rate` (producer rate
limiting) and `ping` (connectivity check) are implemented generically and work for every
broker. `--ndjson` and `--rate` apply wherever the relevant read or write command exists;
`ping` connects via the broker's queue adapter (or its topic adapter for Kafka).

The streaming features are also generic. `forward` relays continuously between two
destinations on the same broker (queue-to-queue for queue brokers, topic-to-topic for
Kafka), with an optional `-x`/`--command` shell command. `--for` (time-bounded streaming) and
`--stats` (live throughput to stderr) apply to every read command and to `forward`, so
any broker can be sampled for a fixed window or monitored for throughput while streaming.

## Traditional Message Brokers

### Apache Artemis

- Protocol: AMQP 1.0
- Creates queues on the fly (e.g. when a consumer connects)
- ANYCAST means traditional queues (default), only one consumer
- MULTICAST means topics & subscriptions, multiple consumers
- Selectors: Full JMS selector support via AMQP source filters
- Management: Jolokia REST API on HTTP port 8161

=> Use message metadata for topology selection.

```mermaid
flowchart LR
    A[Producer] --> B{Address}
    B -->|ANYCAST| C(Consumer1)

    H[Producer] --> I{Address}
    I -->|MULTICAST| J(Consumer1)
    I -->|MULTICAST| K(Consumer2)
```

### RabbitMQ

- Protocol: AMQP 1.0 (RabbitMQ v4+)
- AMQP 1.0 address format: v2 (`/queues/<name>`, `/exchanges/<exchange>/<routing-key>`)
- Queues must be pre-declared (RabbitMQ does not auto-create queues over AMQP 1.0)
- Choose between exchange/queue model (also for topics & subscriptions) and simple queue model
- Choose between `fanout`, `direct`, `topic` and `headers` exchange types
- Topics use exchange-based routing (default exchange: `amq.topic`, configurable via `--exchange/-e`)
- Selectors: Supported via AMQP source filters
- Management: RabbitMQ Management API on HTTP port 15672 (list, purge, stats)

=> Define topology statically by declaring exchanges, queues, and bindings.

```mermaid
flowchart LR
    A[Producer] --> B{Exchange}
    B -->|Binding| C(Queue1) --> D(Consumer1)
    B -->|Binding| E(Queue2) --> F(Consumer2)

    H[Producer] --> I(Queue1) --> J(Consumer1)
```

### IBM MQ

- Protocol: IBM MQ native (requires IBM MQ client libraries)
- Queue-only operations (no topic support in imc)
- Binary name: `imc` (built via `build-imc-in-container.sh` or with `-tags ibmmq`)
- Connection flags include `--qmgr/-m` (queue manager) and `--channel/-c`
- Selectors: IBM MQ message selector support
- TTL: Uses MQMD Expiry field (tenths of a second, converted from ms)
- No management commands (use IBM MQ Explorer or `runmqsc`)
- Build requires IBM MQ SDK/client libraries (platform-specific)

## Streaming Brokers

### Kafka

- Protocol: Kafka native
- Has its own concepts & domain language, which differs from traditional
  messaging and Enterprise Integration Patterns (EIP)
- Always uses topics, no queues
- Always persists messages, ability to replay messages
- TTL: Set as a message header (broker-side retention handles expiry)
- Consumer groups for parallel processing (`--group/-g`)
- Message keys for partitioning (`--key/-K`)
- Management: Topic listing via admin client (no purge/stats)

```mermaid
flowchart LR
    A[Producer] --> B{Topic}
    B -->|Partition| C(Consumer1)
    B -->|Partition| D(Consumer2)
```

### MQTT

- Protocol: MQTT 3.1.1 / 5.0
- Binary: `mmc`, build tag: `mqtt`
- **Queue topology**: send publishes to `queue/{name}` with QoS 1; receive uses MQTT 5.0 shared subscriptions (`$share/xmc/queue/{name}`) for competing consumers; peek subscribes directly without a shared subscription using a fresh clean-session client.
- **Topic topology**: publish/subscribe to MQTT topics directly. Consumer groups via `--group` map to shared subscriptions (`$share/{groupID}/{topic}`).
- TLS: auto-detected via `ssl://` URL scheme or `--tls` flag
- `--client-id` flag: optional, auto-generated if not set
- QoS 0 = non-persistent, QoS 1 = persistent (maps to `--persistent` flag)
- Default server: `tcp://localhost:1883` (env: `MMC_SERVER`)
- Library: `github.com/eclipse/paho.mqtt.golang`

```mermaid
flowchart LR
    A[Producer] -->|queue/name QoS 1| B{$share/xmc/queue/name}
    B --> C(Consumer1)
    B --> D(Consumer2)

    E[Producer] -->|topic| F(Subscriber1)
    E -->|topic| G(Subscriber2)
```

### NATS

- Protocol: NATS Core / JetStream
- Binary: `nmc`, build tag: `nats`
- **Queue topology**: JetStream streams with WorkQueue retention — each message delivered to exactly one consumer. Streams are auto-created on first use (`XMC_Q_{QUEUENAME}`). Peek uses a pull consumer with nak (no acknowledgement) so messages are not consumed.
- **Topic topology**: Core NATS pub/sub subjects. Consumer groups via `--group` flag map to NATS queue subscribers (`QueueSubscribeSync`).
- Request-reply: supported using NATS reply subjects
- TLS: standard flags (`--tls`, `--ca-cert`, `--cert`, `--key-file`, `--insecure`)
- Management: `manage list` enumerates JetStream streams (= queues)
- Default server: `nats://localhost:4222` (env: `NMC_SERVER`)
- Requires JetStream enabled on the server (`--jetstream` flag or `jetstream {}` in server config) for queue operations
- Library: `github.com/nats-io/nats.go`

```mermaid
flowchart LR
    A[Producer] -->|JetStream stream XMC_Q_NAME| B{WorkQueue}
    B -->|pull consumer| C(Consumer1)
    B -->|pull consumer| D(Consumer2)

    E[Producer] -->|subject| F(Subscriber1)
    E -->|subject| G["QueueGroup (--group)"]
    G --> H(Consumer1)
    G --> I(Consumer2)
```

### Apache Pulsar

- Protocol: Pulsar native (binary protocol, port 6650)
- Binary: `pmc`, build tag: `pulsar`
- **Queue topology**: Shared subscription on `persistent://public/default/{queue}` — messages distributed among all subscribers with the same subscription name, each delivered to exactly one consumer.
- **Topic topology**: Exclusive subscription by default (single consumer gets all messages); `--group` maps to Shared subscription for load-balanced consumer groups.
- Durable subscriptions: all subscriptions are durable by default in Pulsar (server retains messages until acknowledged)
- Peek: uses Shared subscription + Nack so messages are redelivered and not consumed
- Request-reply: via ReplyTo topic property
- TLS: auto-detected via `pulsar+ssl://` URL scheme; also `--tls` flag
- Authentication: token-based via `--password` (JWT); TLS client certificate via `--cert`/`--key-file`
- Management: `pmc manage list` uses Pulsar Admin REST API (HTTP port 8080, `--admin-port` to override)
- Default server: `pulsar://localhost:6650` (env: `PMC_SERVER`)
- Tenant/namespace: defaults to `persistent://public/default/`

```mermaid
flowchart LR
    A[Producer] -->|persistent://public/default/queue| B{Shared Sub}
    B -->|competing| C(Consumer1)
    B -->|competing| D(Consumer2)

    E[Producer] -->|persistent://public/default/topic| F{Exclusive Sub}
    F --> G(Consumer1)

    H[Producer] -->|persistent://public/default/topic| I{"Shared Sub (--group)"}
    I --> J(Consumer1)
    I --> K(Consumer2)
```

### Redis

- Protocol: Redis Streams + consumer groups
- Binary: `redmc`, build tag: `redmc`
- **Queue topology**: Redis Streams (`xmc:queue:{name}`) with a single consumer group (`xmc-queue`). `XADD` to send, `XREADGROUP` + `XACK` + `XDEL` to receive (true work-queue semantics). Peek uses `XRANGE` (non-destructive, no ack needed).
- **Topic topology**: Also Redis Streams (`xmc:topic:{name}`) with `MAXLEN ~ 10000` approximate trimming. Independent subscribers use `XREAD` starting from `$` (new-messages-only fan-out, each subscriber tracks its own offset). `--group` maps to consumer groups (`XREADGROUP` + `XACK`, competing consumers within the group). `--durable` groups persist their read offset across reconnections.
- TLS: auto-detected via `rediss://` URL scheme or `--tls` flag
- Application properties: stored with a `p:` prefix in stream entry fields to avoid colliding with reserved metadata fields (`data`, `message-id`, `correlation-id`, `reply-to`, `content-type`)
- Management: `manage list` scans `xmc:queue:*` and `xmc:topic:*` keys; `manage purge` deletes the stream key; `manage stats` uses `XLEN` + `XINFO GROUPS`
- Default server: `redis://localhost:6379` (env: `REDMC_SERVER`)
- Library: `github.com/redis/go-redis/v9`
- **Limitations**: no per-message TTL on streams (Streams have no built-in per-entry expiry); topic Pub/Sub channels are not used (Streams provide persistence and metadata that Pub/Sub lacks)

```mermaid
flowchart LR
    A[Producer] -->|XADD xmc:queue:name| B{Consumer Group xmc-queue}
    B -->|XREADGROUP + XACK + XDEL| C(Consumer1)
    B -->|XREADGROUP + XACK + XDEL| D(Consumer2)

    E[Producer] -->|XADD xmc:topic:name| F(Independent XREAD)
    E -->|XADD xmc:topic:name| G{"Group (--group)"}
    G -->|XREADGROUP + XACK| H(Consumer1)
    G -->|XREADGROUP + XACK| I(Consumer2)
```

### Google Cloud Pub/Sub

- Protocol: gRPC (Google Cloud Pub/Sub API)
- Binary: `gmc`, build tag: `gmc`
- **Queue topology**: A topic + a single shared subscription (`xmc-queue-{name}`) — messages are distributed among competing consumers, each delivered to exactly one. The subscription is auto-created on first `send` so messages are retained before the first `receive`.
- **Topic topology**: Ephemeral per-subscriber subscriptions for true fan-out (auto-deleted on close). `--group` maps to a stable named subscription (competing consumers within the group). `--durable` uses a persistent subscription that retains its read position.
- Peek: uses `Nack` so messages are redelivered
- Authentication: Google Application Default Credentials (ADC) or `--credentials` (service account JSON). No TLS flags (gRPC handles transport).
- Emulator: set `--endpoint` (or env `PUBSUB_EMULATOR_HOST`) for local development
- Management: `manage list` enumerates topics and subscriptions. No purge/stats in v1 (purge requires SeekToTime; stats need Cloud Monitoring API).
- Default project: env `GMC_PROJECT`; no default server (uses Google Cloud)
- Library: `cloud.google.com/go/pubsub`
- **Limitations**: no per-message TTL (retention is subscription-level); no `manage stats` (requires Cloud Monitoring API)

```mermaid
flowchart LR
    A[Producer] -->|Topic| B{Subscription xmc-queue-name}
    B -->|competing pull| C(Consumer1)
    B -->|competing pull| D(Consumer2)

    E[Producer] -->|Topic| F(Ephemeral Sub 1)
    E -->|Topic| G(Ephemeral Sub 2)

    H[Producer] -->|Topic| I{"Named Sub (--group)"}
    I --> J(Consumer1)
    I --> K(Consumer2)
```
