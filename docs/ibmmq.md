# Getting Started with IBM MQ (`imc`)

Binary: **`imc`** — IBM MQ Messaging Client

```bash
go build -tags ibmmq -o imc .
```

> **Note:** Building `imc` requires IBM MQ client libraries (C headers). Use the container-based build script:
> ```bash
> ./scripts/build-imc-in-container.sh
> ```

## Authentication

`imc` connects to an IBM MQ queue manager using a server connection (SVRCONN) channel. The default URL encodes the queue manager, channel, host, and port:

```bash
export IMC_SERVER="ibmmq://localhost:1414/QM1?channel=SYSTEM.DEF.SVRCONN"
```

For authentication:

```bash
export IMC_USER="app"
export IMC_PASSWORD="passw0rd"
```

The queue manager and channel can also be overridden individually:

```bash
export IMC_QUEUE_MANAGER="QM1"
export IMC_CHANNEL="SYSTEM.DEF.SVRCONN"
```

`--qmgr`/`-m` and `--channel`/`-c` flags override the URL components:

```bash
imc -s ibmmq://mqhost:1414 -m MYQMGR -c DEV.APP.SVRCONN -u app -p passw0rd send ...
```

> **Quick start with Docker:**
> ```bash
> docker run -d --name ibmmq -p 1414:1414 -p 9443:9443 \
>   -e LICENSE=accept \
>   -e MQ_QMGR_NAME=QM1 \
>   icr.io/ibm-messaging/mq:latest
> ```
> Default app credentials: `app` / `passw0rd`. Web console: https://localhost:9443

---

## 1. Send and Receive

IBM MQ is queue-centric — send messages to predefined queues and receive from them:

```bash
imc send DEV.QUEUE.1 "Hello World"
imc receive DEV.QUEUE.1
```

Output:
```
Hello World
```

> **Note:** IBM MQ queues must be pre-defined by an MQ administrator. The Docker developer image pre-creates `DEV.QUEUE.1` through `DEV.QUEUE.3`.

---

## 2. IBM MQ-Specific Features

> **Note:** IBM MQ does not have admin/manage commands in xmc. Queue and channel management is done through IBM's own tools (`runmqsc`, MQ Explorer, or the web console). `imc` focuses on messaging operations.

### Queue manager and channel targeting

IBM MQ's connection model revolves around **queue managers** and **SVRCONN channels**. Target different queue managers from the same host:

```bash
imc -m QM1 send DEV.QUEUE.1 "To QM1"
imc -m QM2 send DEV.QUEUE.1 "To QM2"
```

### Message selectors

Filter messages on receive using selectors — IBM MQ supports JMS-style property selection:

```bash
imc send DEV.QUEUE.1 "Urgent task" -P priority=high
imc send DEV.QUEUE.1 "Routine task" -P priority=low
imc receive DEV.QUEUE.1 -S "priority='high'"    # -> Urgent task
```

### Priority

IBM MQ natively supports message priority (0–9):

```bash
imc send DEV.QUEUE.1 "Critical" -Y 9
imc send DEV.QUEUE.1 "Normal" -Y 4
```

Higher-priority messages are delivered first when multiple messages are available.

### TTL / Message Expiry

Set a time-to-live — the message is removed from the queue after it expires:

```bash
imc send DEV.QUEUE.1 "Temporary" -E 30s
```

### Persistent Delivery

Use `-d` for persistent messages (survives queue manager restart):

```bash
imc send DEV.QUEUE.1 "Must not be lost" -d
```

### Peek (non-destructive browse)

Browse messages without consuming them:

```bash
imc peek DEV.QUEUE.1
imc peek DEV.QUEUE.1 -n 5    # peek at up to 5 messages
```

---

## 3. Deep-Dive into xmc Features

### Request / Reply

IBM MQ has strong request/reply support. Start a responder on a request queue:

```bash
imc reply DEV.QUEUE.1 -e -n 0
```

Send a request and wait for the correlated reply:

```bash
imc request DEV.QUEUE.1 "Get account balance"
```

Process each request through a shell command:

```bash
imc reply DEV.QUEUE.1 -x "echo 'Balance: \$42.00'" -n 0
```

### Full Metadata Control

```bash
imc send DEV.QUEUE.1 "Order update" \
  -I "msg-001" \
  -C "order-42" \
  -R "DEV.QUEUE.2" \
  -T application/json \
  -Y 7 \
  -d \
  -E 5m \
  -P customer=acme \
  -P region=emea
```

Receive with JSON output to see all metadata:

```bash
imc receive DEV.QUEUE.1 -J
```

### Custom Output Format

```bash
imc receive DEV.QUEUE.1 -F "[%Y] %c | %h | %s\n"
```

### Move and Forward

Redrive messages from a dead-letter queue:

```bash
imc move SYSTEM.DEAD.LETTER.QUEUE DEV.QUEUE.1 -n 0
```

Continuously forward messages between queues:

```bash
imc forward DEV.QUEUE.1 DEV.QUEUE.2
```

Forward with transformation:

```bash
imc forward incoming processed -x "jq '.status = \"reviewed\"'"
```

### Bulk and Streaming

```bash
seq 100 | imc send DEV.QUEUE.1 -l --rate 10
imc receive DEV.QUEUE.1 -n 0 --stats
```

---

## 4. Bridge to Another Broker

Stream from IBM MQ to RabbitMQ using NDJSON:

```bash
# Drain an IBM MQ queue into a RabbitMQ queue
imc receive DEV.QUEUE.1 -n 0 --ndjson | rmc send target --ndjson

# Continuous bridge (wait for new messages as they arrive)
imc receive DEV.QUEUE.1 -w -n 0 --ndjson | rmc send target --ndjson
```

NDJSON preserves message ID, correlation ID, content type, application properties, and payload — everything transfers cleanly between IBM MQ and RabbitMQ despite the different underlying protocols.
