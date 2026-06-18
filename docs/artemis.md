# Getting Started with Apache Artemis (`amc`)

Binary: **`amc`** — Artemis Messaging Client (AMQP 1.0)

```bash
go build -tags artemis -o amc .
```

## Authentication

`amc` connects via AMQP 1.0 with SASL PLAIN authentication. The default server is `amqp://localhost:5672`.

```bash
export AMC_SERVER="amqp://localhost:5672"
export AMC_USER="artemis"
export AMC_PASSWORD="artemis"
```

Or pass credentials inline: `amc -s amqp://localhost:5672 -u artemis -p artemis ...`

For TLS use `amqps://` or add `--tls`. See `amc --help` for full TLS options.

> **Quick start with Docker:**
> ```bash
> docker run -d --name artemis -p 5672:5672 -p 8161:8161 apache/activemq-artemis:latest-alpine
> ```
> Default credentials: `artemis` / `artemis`. Web console: http://localhost:8161

---

## 1. Send and Receive

Artemis auto-creates queues and topics on first use — no pre-declaration needed.

```bash
amc send myqueue "Hello World"
amc receive myqueue
```

Output:
```
Hello World
```

---

## 2. Admin Commands and ANYCAST / MULTICAST

Artemis distinguishes between **ANYCAST** (point-to-point queues) and **MULTICAST** (pub/sub topics). xmc maps `create-queue` to ANYCAST and `create-topic` to MULTICAST.

### Create and inspect queues and topics

```bash
amc manage create-queue orders
amc manage create-topic notifications
amc manage list
```

The list output shows both queues and topics with their message counts.

### Queue stats and purge

```bash
amc send orders "Order A"
amc send orders "Order B"
amc manage stats orders           # Messages: 2
amc manage purge orders           # Purged 2 messages
amc manage stats orders           # Messages: 0
```

### Topic pub/sub with multiple subscribers

Start two subscribers in separate terminals:

```bash
# Terminal 1
amc subscribe notifications -D -g group-a -n 0

# Terminal 2
amc subscribe notifications -D -g group-b -n 0
```

Publish a message — both subscribers receive it (MULTICAST):

```bash
amc publish notifications "System maintenance at 02:00"
```

Each consumer group gets its own copy. Within a group, messages are load-balanced.

### Clean up

```bash
amc manage delete-queue orders
amc manage delete-topic notifications
```

**Available manage commands:** `list`, `purge`, `stats`, `create-queue`, `delete-queue`, `create-topic`, `delete-topic`.

Management uses the Jolokia REST API on port 8161 (derived from the AMQP host).

---

## 3. Deep-Dive into xmc Features

### Request / Reply

Start a responder that echoes requests back:

```bash
amc reply requests -e -n 0
```

In another terminal:

```bash
amc request requests "ping"
```

The responder sends back "ping" with a matching correlation ID. Use `-x` to process requests dynamically:

```bash
amc reply requests -x "date '+%Y-%m-%d %H:%M:%S'" -n 0
```

### Message Properties and Selectors

Send messages with application properties:

```bash
amc send tasks "Deploy prod" -P env=prod -P team=ops
amc send tasks "Run tests" -P env=staging -P team=dev
```

Filter on receive using JMS-style selectors:

```bash
amc receive tasks -S "env='prod'"     # -> Deploy prod
amc receive tasks -S "team='dev'"     # -> Run tests
```

### Full metadata control

```bash
amc send myqueue "Important" \
  -I "msg-001" \
  -C "batch-42" \
  -T application/json \
  -Y 9 \
  -d \
  -E 60s
```

- `-I` message ID, `-C` correlation ID, `-T` content type
- `-Y 9` priority (0–9), `-d` persistent delivery
- `-E 60s` time-to-live (message expires after 60 seconds)

### Move and Forward

Redrive messages from a dead-letter queue:

```bash
amc move DLQ orders -n 0
```

Continuously forward messages, transforming each with a shell command:

```bash
amc forward raw processed -x "jq '.status = \"done\"'"
```

### Bulk and Streaming

```bash
seq 1000 | amc send loadtest -l --rate 50        # 50 msg/s
amc receive loadtest -n 0 --stats                 # drain with throughput stats
amc subscribe events --for 1m --stats              # stream for 1 minute
```

### Custom Output Format

```bash
amc receive myqueue -F "[%i] %p{env}: %s\n"
```

---

## 4. Bridge to Another Broker

Stream from Artemis to RabbitMQ using NDJSON:

```bash
# One-shot: drain an Artemis queue into a RabbitMQ queue
amc receive myqueue -n 0 --ndjson | rmc send target --ndjson

# Continuous: relay Artemis topic messages to RabbitMQ
amc subscribe events -n 0 --ndjson | rmc send events --ndjson
```

NDJSON preserves message ID, correlation ID, content type, properties, and payload across brokers.
