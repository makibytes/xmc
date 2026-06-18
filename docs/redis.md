# Getting Started with Redis (`redmc`)

Binary: **`redmc`** — Redis Messaging Client (Redis Streams)

```bash
go build -tags redis -o redmc .
```

## Authentication

`redmc` connects to Redis using the standard Redis URL scheme. The default server is `redis://localhost:6379`.

```bash
export REDMC_SERVER="redis://localhost:6379"
```

For password-protected Redis:

```bash
export REDMC_SERVER="redis://:mypassword@localhost:6379"
# or
export REDMC_USER="default"
export REDMC_PASSWORD="mypassword"
```

For TLS add `--tls` or use `rediss://` in the URL. See `redmc --help` for cert options.

> **Quick start with Docker:**
> ```bash
> docker run -d --name redis -p 6379:6379 redis:latest
> ```

---

## 1. Send and Receive

```bash
redmc send myqueue "Hello World"
redmc receive myqueue
```

Output:
```
Hello World
```

Under the hood, xmc models queues as Redis Streams with consumer groups. The stream key is `xmc:queue:myqueue`.

---

## 2. Admin Commands and Redis Streams

Redis Streams are the backbone for both queues and topics in xmc. Queues use a shared consumer group for competing-consumer semantics; topics use per-subscriber consumer groups for fan-out.

### Create queues and topics explicitly

```bash
redmc manage create-queue orders
redmc manage create-topic events
redmc manage list
```

The list output shows both queues (prefixed `xmc:queue:`) and topics (prefixed `xmc:topic:`) with message counts.

### Stats and purge

```bash
redmc send orders "Order A"
redmc send orders "Order B"
redmc manage stats orders       # Messages: 2, Consumers: 0
redmc manage purge orders       # Purged 2 messages
```

### Topic pub/sub with consumer groups

Start subscribers in separate terminals — each group gets every message:

```bash
# Terminal 1
redmc subscribe events -g analytics -n 0

# Terminal 2
redmc subscribe events -g alerting -n 0
```

Publish a message:

```bash
redmc publish events "user.signup"
```

Both `analytics` and `alerting` groups receive the message. Within a group, messages are load-balanced across consumers.

### Clean up

```bash
redmc manage delete-queue orders
redmc manage delete-topic events
```

**Available manage commands:** `list`, `purge`, `stats`, `create-queue`, `delete-queue`, `create-topic`, `delete-topic`.

---

## 3. Deep-Dive into xmc Features

### Request / Reply

Start an echo responder:

```bash
redmc reply requests -e -n 0
```

Send a request and wait for the reply:

```bash
redmc request requests "What time is it?"
```

Process requests with a shell command:

```bash
redmc reply requests -x "wc -c" -n 0    # reply with byte count
```

### Message Properties

Tag messages with application properties:

```bash
redmc send tasks "Deploy" -P env=prod -P region=us-east
```

Receive with JSON output to inspect all metadata:

```bash
redmc receive tasks -J
```

### Custom Output Format

```bash
redmc receive myqueue -F "%i | %h | %s\n"
```

### Move and Forward

Redrive from a dead-letter queue:

```bash
redmc move failed orders -n 0
```

Continuous forwarding with transformation:

```bash
redmc forward raw processed -x "tr '[:lower:]' '[:upper:]'"
```

### Bulk and Streaming

```bash
seq 500 | redmc send loadtest -l --rate 25
redmc receive loadtest -n 0 --stats
redmc subscribe events --for 30s --stats
```

### Durable Subscriptions

Use `-D` to create a durable subscription that survives disconnects:

```bash
redmc subscribe events -D -g my-durable-group -n 0
```

---

## 4. Bridge to Another Broker

Stream from Redis to RabbitMQ using NDJSON:

```bash
# Drain a Redis queue into a RabbitMQ queue
redmc receive myqueue -n 0 --ndjson | rmc send target --ndjson

# Continuous bridge from Redis topic to RabbitMQ
redmc subscribe events -n 0 --ndjson | rmc send events --ndjson
```

NDJSON preserves message ID, correlation ID, content type, properties, and payload across brokers.
