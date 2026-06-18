# Getting Started with Azure Service Bus (`azmc`)

Binary: **`azmc`** — Azure Service Bus Messaging Client

```bash
go build -tags azure -o azmc .
```

## Authentication

`azmc` connects to Azure Service Bus using either a **connection string** (most common) or **Azure AD** credentials.

### Connection string (90% of users)

```bash
export AZMC_CONNECTION_STRING="Endpoint=sb://mynamespace.servicebus.windows.net/;SharedAccessKeyName=RootManageSharedAccessKey;SharedAccessKey=..."
```

The `-s` flag maps to `--connection-string`:

```bash
azmc -s "Endpoint=sb://..." send myqueue "Hello"
```

### Azure AD / Managed Identity

```bash
export AZMC_NAMESPACE="mynamespace.servicebus.windows.net"
az login    # or use managed identity on Azure VMs/AKS
```

One of `--connection-string` or `--namespace` is required.

---

## 1. Send and Receive

```bash
azmc send myqueue "Hello World"
azmc receive myqueue
```

Output:
```
Hello World
```

Azure Service Bus has native queue and topic support — no auto-creation. Create queues and topics in the Azure Portal, Azure CLI, or use Azure's management SDKs.

---

## 2. Admin Commands and Azure Service Bus Features

### List, stats, and purge

```bash
azmc manage list                  # lists all queues and topics
azmc manage stats myqueue         # active + total message counts
azmc manage purge myqueue         # drains all messages from the queue
```

### Topics and subscriptions

Azure Service Bus topics work like RabbitMQ's fan-out — each subscription gets a copy of every message. Create topics and subscriptions via the Azure Portal or CLI, then use `azmc`:

```bash
# Terminal 1
azmc subscribe notifications -g team-ops -n 0

# Terminal 2
azmc subscribe notifications -g team-dev -n 0
```

Publish to the topic:

```bash
azmc publish notifications "Deployment complete"
```

Both subscribers receive the message.

### Native peek

Azure Service Bus supports non-destructive peek at the protocol level:

```bash
azmc peek myqueue                 # view the next message without consuming it
azmc peek myqueue -n 5            # peek at up to 5 messages
```

### TTL / Message Expiry

Set a per-message time-to-live — the message expires and is removed (or dead-lettered) after the duration:

```bash
azmc send myqueue "Temporary alert" -E 5m
```

**Available manage commands:** `list`, `purge`, `stats`.

---

## 3. Deep-Dive into xmc Features

### Request / Reply

Start a responder:

```bash
azmc reply requests -e -n 0
```

Send a request and wait for the reply:

```bash
azmc request requests "Health check"
```

Process requests dynamically:

```bash
azmc reply requests -x "echo 'OK'" -n 0
```

### Message Properties

Attach application properties to messages:

```bash
azmc send tasks "Deploy" -P env=prod -P region=westeurope
```

Receive with JSON output:

```bash
azmc receive tasks -J
```

### Custom Output Format

```bash
azmc receive tasks -F "[%i] %p{env}: %s\n"
```

### Full Metadata Control

```bash
azmc send myqueue "Important" \
  -I "msg-001" \
  -C "batch-42" \
  -T application/json \
  -d \
  -E 1h
```

### Move and Forward

Redrive messages from a dead-letter queue:

```bash
azmc move "myqueue/$deadletterqueue" myqueue -n 0
```

> **Tip:** Azure Service Bus dead-letter queues are accessed by appending `/$deadletterqueue` to the queue name.

Continuous forwarding with transformation:

```bash
azmc forward incoming processed -x "jq '.reviewed = true'"
```

### Durable Subscriptions

Azure subscriptions are durable by default:

```bash
azmc subscribe events -D -g my-durable-sub -n 0
```

### Bulk and Streaming

```bash
seq 100 | azmc send loadtest -l --rate 10
azmc receive loadtest -n 0 --stats
azmc subscribe events --for 30s --stats
```

---

## 4. Bridge to Another Broker

Stream from Azure Service Bus to RabbitMQ using NDJSON:

```bash
# Drain an Azure queue into a RabbitMQ queue
azmc receive myqueue -n 0 --ndjson | rmc send target --ndjson

# Continuous bridge from Azure topic to RabbitMQ
azmc subscribe events -n 0 --ndjson | rmc send events --ndjson
```

NDJSON preserves message ID, correlation ID, content type, properties, and payload across brokers.
