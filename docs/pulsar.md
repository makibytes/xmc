# Getting Started with Apache Pulsar (`pmc`)

Binary: **`pmc`** — Pulsar Messaging Client

```bash
go build -tags pulsar -o pmc .
```

## Authentication

`pmc` connects to Pulsar using the native Pulsar protocol. The default server is `pulsar://localhost:6650`.

```bash
export PMC_SERVER="pulsar://localhost:6650"
```

For token-based authentication, pass the token via `--password`:

```bash
export PMC_PASSWORD="eyJhbGciOiJSUzI1NiJ9..."
export PMC_USER="token"    # any non-empty value triggers token auth
```

For TLS use `pulsar+ssl://` or add `--tls`. For mTLS, use `--cert` and `--key-file` (switches to TLS certificate authentication).

> **Quick start with Docker:**
> ```bash
> docker run -d --name pulsar -p 6650:6650 -p 8080:8080 apachepulsar/pulsar:latest bin/pulsar standalone
> ```
> Admin API: http://localhost:8080

---

## 1. Send and Receive

Pulsar supports both queue-style (competing consumers) and topic-style (fan-out) messaging.

Queue-style — send and receive from a shared subscription:

```bash
pmc send myqueue "Hello World"
pmc receive myqueue
```

Output:
```
Hello World
```

Topic-style — publish and subscribe:

```bash
# Terminal 1
pmc subscribe mytopic -n 1

# Terminal 2
pmc publish mytopic "Hello World"
```

---

## 2. Admin Commands and Pulsar Topics

Pulsar topics live in the `persistent://public/default` namespace. Management uses the Pulsar Admin REST API.

### Create topics

```bash
pmc manage create-topic orders
pmc manage list
```

Create a partitioned topic:

```bash
pmc manage create-topic events --partitions 4
```

The `--admin-port` flag (default 8080) controls which port the admin API is reached on:

```bash
pmc manage list --admin-port 8443
```

### Subscription types

Pulsar's subscription model determines how messages are distributed:

- **Shared** (queue-style): multiple consumers share the load. This is what `pmc send`/`receive` uses (subscription name: `xmc-queue`).
- **Exclusive** (default for subscribe): one consumer owns the subscription.
- **Shared with group**: `pmc subscribe -g mygroup` uses a Shared subscription.

Start two consumers in the same group for load balancing:

```bash
# Terminal 1
pmc subscribe events -g processors -n 0

# Terminal 2
pmc subscribe events -g processors -n 0
```

Both consumers share the workload within the `processors` subscription.

### Clean up

```bash
pmc manage delete-topic orders
pmc manage delete-topic events
```

**Available manage commands:** `list`, `create-topic` (with `--partitions`), `delete-topic`. Use `--admin-port` to override the admin API port.

---

## 3. Deep-Dive into xmc Features

### Request / Reply

Start a responder:

```bash
pmc reply requests -e -n 0
```

Send a request:

```bash
pmc request requests "What is the status?"
```

Process each request through a shell command:

```bash
pmc reply requests -x "echo 'OK'" -n 0
```

### Message Properties

Attach application properties to messages:

```bash
pmc send myqueue "Deploy to prod" -P env=prod -P team=platform
```

Receive with JSON output:

```bash
pmc receive myqueue -J
```

### Custom Output Format

```bash
pmc receive myqueue -F "%i | %h | %s\n"
```

### Move and Forward

Move messages between queues:

```bash
pmc move failed orders -n 0
```

Continuously forward messages:

```bash
pmc forward incoming processed
```

Forward with transformation:

```bash
pmc forward raw clean -x "jq '.processed = true'"
```

### Durable Subscriptions

Create a durable subscription that survives disconnects:

```bash
pmc subscribe events -D -g my-durable-sub -n 0
```

Reconnecting with the same group name resumes from the last acknowledged message.

### Bulk and Streaming

```bash
seq 500 | pmc send loadtest -l --rate 50
pmc receive loadtest -n 0 --stats
pmc subscribe events --for 1m --stats
```

---

## 4. Bridge to Another Broker

Stream from Pulsar to RabbitMQ using NDJSON:

```bash
# Drain a Pulsar queue into a RabbitMQ queue
pmc receive myqueue -n 0 --ndjson | rmc send target --ndjson

# Continuous bridge from Pulsar topic to RabbitMQ
pmc subscribe events -n 0 --ndjson | rmc send events --ndjson
```

NDJSON preserves message ID, correlation ID, content type, properties, and payload across brokers.
