# Getting Started with RabbitMQ (`rmc`)

Binary: **`rmc`** — RabbitMQ Messaging Client (AMQP 1.0)

```bash
go build -tags rabbitmq -o rmc .
```

## Authentication

`rmc` connects via AMQP 1.0 with SASL PLAIN authentication. The default server is `amqp://localhost:5672`.

```bash
export RMC_SERVER="amqp://localhost:5672"
export RMC_USER="guest"
export RMC_PASSWORD="guest"
```

Or pass credentials on every command: `rmc -s amqp://localhost:5672 -u guest -p guest ...`

For TLS use `amqps://` or add `--tls`. See `rmc --help` for `--ca-cert`, `--cert`, `--key-file`, `--insecure`.

> **Quick start with Docker:**
> ```bash
> docker run -d --name rabbitmq -p 5672:5672 -p 15672:15672 rabbitmq:4-management
> ```

---

## 1. Send and Receive

Send a message to a queue and receive it:

```bash
rmc send myqueue "Hello World"
rmc receive myqueue
```

Output:
```
Hello World
```

Queues in RabbitMQ must exist before use. Create one first with `rmc manage create-queue myqueue` (see below), or configure the broker to auto-create queues.

---

## 2. Exchanges, Bindings, and Admin Commands

RabbitMQ's distinguishing feature is **exchanges** with **bindings** and **routing keys** — messages published to an exchange are routed to bound queues based on the exchange type and routing rules.

### Fan-out: duplicate messages to multiple queues

Create a fanout exchange and two queues, bind both to the exchange:

```bash
rmc manage create-exchange broadcast --type fanout
rmc manage create-queue inbox-alice
rmc manage create-queue inbox-bob
rmc manage bind-queue inbox-alice broadcast
rmc manage bind-queue inbox-bob broadcast
```

Publish through the exchange — the message is duplicated to both queues:

```bash
rmc publish anything -e broadcast "Hello everyone"
```

Receive from each queue independently:

```bash
rmc receive inbox-alice    # -> Hello everyone
rmc receive inbox-bob      # -> Hello everyone
```

### Topic exchange: routing-key pattern matching

Create a topic exchange and bind queues with routing-key patterns:

```bash
rmc manage create-exchange logs --type topic
rmc manage create-queue all-logs
rmc manage create-queue errors-only
rmc manage bind-queue all-logs logs --routing-key "#"
rmc manage bind-queue errors-only logs --routing-key "*.error"
```

Publish with different routing keys:

```bash
rmc publish "app.error" -e logs "Something went wrong"
rmc publish "app.info" -e logs "All good"
```

Drain each queue to see the routing in action:

```bash
rmc receive all-logs -n 0       # -> both messages
rmc receive errors-only -n 0    # -> only the error
```

### Inspect and clean up

```bash
rmc manage list                          # list all queues with message counts
rmc manage stats inbox-alice             # detailed stats for one queue
rmc manage purge inbox-alice             # remove all messages from a queue
rmc manage unbind-queue all-logs logs --routing-key "#"
rmc manage unbind-queue errors-only logs --routing-key "*.error"
rmc manage delete-exchange logs
rmc manage delete-exchange broadcast
rmc manage delete-queue inbox-alice
rmc manage delete-queue inbox-bob
rmc manage delete-queue all-logs
rmc manage delete-queue errors-only
```

**Available manage commands:** `list`, `purge`, `stats`, `create-queue`, `delete-queue`, `create-exchange` (with `--type`), `delete-exchange`, `bind-queue` (with `--routing-key`), `unbind-queue` (with `--routing-key`).

---

## 3. Deep-Dive into xmc Features

### Request / Reply

The request-reply pattern lets one process send a request and wait for a correlated response.

In one terminal, start a responder that echoes back whatever it receives:

```bash
rmc reply requests -e -n 0
```

In another terminal, send a request and wait for the reply:

```bash
rmc request requests "What is 2+2?"
```

The responder echoes the payload back. `rmc request` auto-generates a reply-to address and correlation ID; `rmc reply -e` sends the response to the correct reply-to with matching correlation.

For dynamic processing, pipe each request through a shell command:

```bash
rmc reply requests -x "tr '[:lower:]' '[:upper:]'" -n 0
```

Now every request is uppercased before being returned as the reply.

### Message Properties and Metadata

Set application properties (JMS-style) and protocol metadata:

```bash
rmc send myqueue "Order #42" \
  -P customer=acme \
  -P priority=high \
  -C order-42 \
  -I "msg-001" \
  -T application/json \
  -Y 9 \
  -d
```

- `-P key=value` — application properties (repeatable)
- `-C` — correlation ID
- `-I` — message ID
- `-T` — content type
- `-Y 9` — priority (0–9)
- `-d` — persistent delivery

Receive with JSON output to see all metadata:

```bash
rmc receive myqueue -J
```

### Selectors

Filter messages by application properties using JMS-style selectors:

```bash
rmc send tasks "Deploy prod" -P env=prod
rmc send tasks "Deploy staging" -P env=staging
rmc receive tasks -S "env='prod'"    # -> Deploy prod
```

### Custom Output Format

Use kcat-style format strings for structured output:

```bash
rmc receive myqueue -F "%i | %c | %h | %s\n"
```

Tokens: `%s` payload, `%i` message-id, `%c` correlation-id, `%h` all properties, `%p{key}` one property, `%r` reply-to, `%y` content-type, `%S` payload length.

### Move and Forward

Move messages between queues (dead-letter redrive):

```bash
rmc move deadletter myqueue -n 0    # move all from deadletter back to myqueue
```

Forward continuously relays messages as they arrive:

```bash
rmc forward source destination
```

Transform each message through a shell command during forwarding:

```bash
rmc forward raw processed -x "jq '.status = \"reviewed\"'"
```

### Bulk and Streaming

Send many messages at a controlled rate:

```bash
seq 100 | rmc send loadtest -l --rate 10    # 10 msg/s, one per line from stdin
```

Receive with throughput stats:

```bash
rmc receive loadtest -n 0 --stats
```

Stream for a bounded duration:

```bash
rmc subscribe "events.#" --for 30s --stats
```

---

## 4. Bridge to Another Broker

Use xmc's NDJSON format to losslessly stream messages between brokers. This example bridges RabbitMQ to Kafka:

```bash
# Drain a RabbitMQ queue and load it into a Kafka topic
rmc receive myqueue -n 0 --ndjson | kmc publish target-topic --ndjson
```

For continuous bridging:

```bash
rmc subscribe "events.#" --ndjson -n 0 | kmc publish events --ndjson
```

The `--ndjson` format preserves message ID, correlation ID, reply-to, content type, properties, and payload — everything except broker-specific internals.
