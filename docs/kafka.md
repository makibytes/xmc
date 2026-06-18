# Getting Started with Apache Kafka (`kmc`)

Binary: **`kmc`** — Kafka Messaging Client

```bash
go build -tags kafka -o kmc .
```

## Authentication

`kmc` connects to Kafka brokers using the native Kafka protocol. The default server is `kafka://localhost:9092`.

```bash
export KMC_SERVER="kafka://localhost:9092"
```

For SASL PLAIN authentication (both must be set):

```bash
export KMC_USER="my-user"
export KMC_PASSWORD="my-password"
```

For TLS use `kafka+ssl://` or add `--tls`. Multiple brokers: `kafka://broker1:9092,broker2:9092`.

> **Quick start with Docker:**
> ```bash
> docker run -d --name kafka -p 9092:9092 apache/kafka:latest
> ```

---

## 1. Publish and Subscribe

Kafka is topic-centric — there are no queues. The natural operations are publish and subscribe.

```bash
kmc publish mytopic "Hello World"
kmc subscribe mytopic
```

Output:
```
Hello World
```

Subscribe blocks by default (`--wait` is on) and waits for messages. Press Ctrl-C to stop, or use `-n 1` to receive exactly one message.

---

## 2. Admin Commands and Kafka Topics

### Create a topic with partitions

```bash
kmc manage create-topic orders --partitions 3 --replication-factor 1
kmc manage list
```

The list shows all topics with their partition counts.

### Consumer groups for parallel processing

Kafka's consumer groups distribute partitions across consumers. Start two subscribers in the same group:

```bash
# Terminal 1
kmc subscribe orders -g order-processors -n 0

# Terminal 2
kmc subscribe orders -g order-processors -n 0
```

Publish messages — they are distributed across the two consumers (each partition assigned to one consumer):

```bash
for i in $(seq 1 10); do kmc publish orders "Order $i"; done
```

Start a second group to independently consume the same topic:

```bash
kmc subscribe orders -g analytics -n 0
```

Both `order-processors` and `analytics` receive all messages, but within `order-processors` the load is split.

### Message keys for partition affinity

Use `-K` to set a message key — messages with the same key always go to the same partition:

```bash
kmc publish orders -K "customer-42" "Order for customer 42"
kmc publish orders -K "customer-42" "Another order for customer 42"
kmc publish orders -K "customer-99" "Order for customer 99"
```

### Topic configuration

Pass Kafka-native topic configs at creation time:

```bash
kmc manage create-topic logs \
  --partitions 6 \
  --replication-factor 1 \
  --config retention.ms=86400000 \
  --config cleanup.policy=delete
```

### Clean up

```bash
kmc manage delete-topic orders
kmc manage delete-topic logs
```

**Available manage commands:** `list`, `create-topic` (with `--partitions`, `--replication-factor`, `--config`), `delete-topic`.

---

## 3. Deep-Dive into xmc Features

> **Note:** Kafka is topic-only in xmc — queue commands (`send`, `receive`, `peek`, `request`, `reply`, `move`) are not available. Use `publish`, `subscribe`, and `forward` instead.

### Message Properties

Attach application properties (Kafka headers) to messages:

```bash
kmc publish events "User signed up" -P source=web -P region=eu
```

Subscribe with JSON output to see properties:

```bash
kmc subscribe events -J -n 1
```

### Custom Output Format

```bash
kmc subscribe events -n 0 -F "%p{source} [%y]: %s\n"
```

### Forward between topics

Continuously relay messages from one topic to another:

```bash
kmc forward raw-events clean-events
```

Transform each message during forwarding:

```bash
kmc forward raw-events clean-events -x "jq '{event: .type, ts: .timestamp}'"
```

### Bulk Publishing

```bash
seq 1000 | kmc publish loadtest -l --rate 100    # 100 msg/s, one per line
```

### Streaming with Stats

```bash
kmc subscribe mytopic -n 0 --for 30s --stats
```

### TTL / Message Expiry

Set a per-message TTL:

```bash
kmc publish events "temporary" -E 10s
```

### Durable Consumption

The consumer group offset is managed by Kafka — restart a subscriber with the same `-g` group and it resumes from where it left off.

---

## 4. Bridge to Another Broker

Stream Kafka topics into RabbitMQ using NDJSON:

```bash
# One-shot: drain a Kafka topic into a RabbitMQ queue
kmc subscribe mytopic -n 0 --ndjson | rmc send target --ndjson

# Continuous bridge
kmc subscribe events -n 0 --ndjson | rmc send events --ndjson
```

NDJSON preserves message ID, correlation ID, content type, properties, and payload across brokers.
