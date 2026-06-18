# Getting Started with NATS (`nmc`)

Binary: **`nmc`** — NATS Messaging Client (NATS / JetStream)

```bash
go build -tags nats -o nmc .
```

## Authentication

`nmc` connects to NATS using the native NATS protocol. The default server is `nats://localhost:4222`.

```bash
export NMC_SERVER="nats://localhost:4222"
```

For username/password authentication:

```bash
export NMC_USER="myuser"
export NMC_PASSWORD="mypassword"
```

For TLS add `--tls`. See `nmc --help` for cert options.

> **Quick start with Docker:**
> ```bash
> docker run -d --name nats -p 4222:4222 nats:latest -js
> ```
> The `-js` flag enables JetStream (required for queue operations).

---

## 1. Send and Receive

NATS queue operations use JetStream streams under the hood:

```bash
nmc send myqueue "Hello World"
nmc receive myqueue
```

Output:
```
Hello World
```

Topic operations use core NATS (fire-and-forget pub/sub):

```bash
# Terminal 1 (start subscriber first — core NATS has no persistence)
nmc subscribe mytopic -n 1

# Terminal 2
nmc publish mytopic "Hello World"
```

---

## 2. Admin Commands and JetStream Streams

xmc models NATS queues as **JetStream streams** — durable, persistent message storage.

### Create a stream with custom settings

```bash
nmc manage create-queue orders
nmc manage list
```

The default retention policy is **WorkQueue** (messages are removed after acknowledgment).

Create a stream with different retention:

```bash
nmc manage create-queue events --retention limits --max-msgs 10000
```

- `--retention workqueue` — messages deleted after ack (default, competing consumers)
- `--retention limits` — messages kept until limits are hit (replay-friendly)
- `--retention interest` — messages deleted when all consumers have acked

### Custom subjects

By default, a stream named `myqueue` listens on subject `xmc.queue.myqueue`. Override this:

```bash
nmc manage create-queue orders --subject "orders.>" --subject "returns.>"
```

Now the stream captures messages published to any subject matching `orders.>` or `returns.>`.

### Clean up

```bash
nmc manage delete-queue orders
nmc manage delete-queue events
```

**Available manage commands:** `list`, `create-queue` (with `--retention`, `--max-msgs`, `--subject`), `delete-queue`.

---

## 3. Deep-Dive into xmc Features

### Request / Reply

NATS has native request/reply support. Start a responder:

```bash
nmc reply requests -e -n 0
```

Send a request:

```bash
nmc request requests "ping"
```

Process requests with a command:

```bash
nmc reply requests -x "date" -n 0
```

### Move and Forward

Move messages between queues:

```bash
nmc move failed orders -n 0
```

Continuously forward messages:

```bash
nmc forward incoming processed
```

Forward with transformation:

```bash
nmc forward raw clean -x "jq '.status = \"processed\"'"
```

### Bulk and Streaming

```bash
seq 100 | nmc send loadtest -l --rate 10
nmc receive loadtest -n 0 --stats
```

Stream a topic for a bounded duration:

```bash
nmc subscribe events --for 30s --stats
```

### Custom Output Format

```bash
nmc receive myqueue -F "[%i] %s\n"
```

> **Note:** NATS does not support application properties (`-P`) or message selectors (`-S`) at the protocol level.

---

## 4. Bridge to Another Broker

Stream from NATS to RabbitMQ using NDJSON:

```bash
# Drain a NATS JetStream queue into a RabbitMQ queue
nmc receive myqueue -n 0 --ndjson | rmc send target --ndjson

# Continuous bridge from NATS topic to RabbitMQ
nmc subscribe events -n 0 --ndjson | rmc send events --ndjson
```

NDJSON preserves message payload and available metadata across brokers.
