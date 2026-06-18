# Getting Started with Google Cloud Pub/Sub (`gmc`)

Binary: **`gmc`** — Google Pub/Sub Messaging Client

```bash
go build -tags google -o gmc .
```

## Authentication

`gmc` uses the Google Cloud Pub/Sub API. Authentication follows the standard Google Cloud model.

```bash
export GMC_PROJECT="my-gcp-project"
```

The `-s` flag maps to `--project` (your GCP project ID). Authentication uses **Application Default Credentials (ADC)** — the same credentials `gcloud` uses:

```bash
gcloud auth application-default login
```

Or point to a service account JSON file:

```bash
export GMC_CREDENTIALS="/path/to/service-account.json"
```

For the Pub/Sub emulator:

```bash
export GMC_SERVER="localhost:8085"    # sets PUBSUB_EMULATOR_HOST internally
```

---

## 1. Send and Receive

Pub/Sub topics and subscriptions are auto-created by `gmc` on first use.

```bash
gmc send myqueue "Hello World"
gmc receive myqueue
```

Output:
```
Hello World
```

Under the hood, `gmc` models queues as **subscriptions** and uses topics for pub/sub:

```bash
# Terminal 1
gmc subscribe mytopic -n 1

# Terminal 2
gmc publish mytopic "Hello World"
```

---

## 2. Topics, Subscriptions, and Admin Commands

### List existing resources

```bash
gmc manage list
```

This lists all topics and subscriptions in the project.

### Auto-creation behavior

Unlike self-hosted brokers, `gmc` automatically creates topics and subscriptions when you send, receive, publish, or subscribe. There are no `create-*` or `delete-*` manage commands — use the GCP Console or `gcloud` CLI for lifecycle management.

### Multiple subscribers (fan-out)

Each subscription gets its own copy of every message published to a topic. Create independent consumers by using different group names:

```bash
# Terminal 1
gmc subscribe events -g analytics -n 0

# Terminal 2
gmc subscribe events -g alerting -n 0
```

Publish a message — both subscribers receive it:

```bash
gmc publish events "User signed up"
```

### Competing consumers within a group

Multiple instances of the same group share the load:

```bash
# Terminal 1
gmc subscribe events -g processors -n 0

# Terminal 2 (same group)
gmc subscribe events -g processors -n 0
```

Messages are distributed across both consumers within the `processors` subscription.

**Available manage commands:** `list` only. Use `gcloud pubsub` or the GCP Console for topic/subscription lifecycle management.

---

## 3. Deep-Dive into xmc Features

### Request / Reply

Start a responder:

```bash
gmc reply requests -e -n 0
```

Send a request:

```bash
gmc request requests "What is the status?"
```

Process requests dynamically:

```bash
gmc reply requests -x "echo 'All systems operational'" -n 0
```

### Message Properties

Attach application properties (Pub/Sub attributes):

```bash
gmc send myqueue "Deploy to prod" -P env=prod -P team=platform
```

Receive with JSON output to see all attributes:

```bash
gmc receive myqueue -J
```

### Custom Output Format

```bash
gmc receive myqueue -F "%p{env}: %s\n"
```

### Move and Forward

Move messages between queues (subscriptions):

```bash
gmc move failed orders -n 0
```

Continuous forwarding:

```bash
gmc forward incoming processed
```

Forward with transformation:

```bash
gmc forward raw clean -x "jq '.status = \"reviewed\"'"
```

### Durable Subscriptions

Pub/Sub subscriptions are durable by default — messages are retained until acknowledged. Use `-D` to make this explicit in `subscribe`:

```bash
gmc subscribe events -D -g my-durable-group -n 0
```

### Bulk and Streaming

```bash
seq 100 | gmc send loadtest -l --rate 10
gmc receive loadtest -n 0 --stats
gmc subscribe events --for 30s --stats
```

---

## 4. Bridge to Another Broker

Stream from Google Pub/Sub to RabbitMQ using NDJSON:

```bash
# Drain a Pub/Sub subscription into a RabbitMQ queue
gmc receive myqueue -n 0 --ndjson | rmc send target --ndjson

# Continuous bridge from Pub/Sub topic to RabbitMQ
gmc subscribe events -n 0 --ndjson | rmc send events --ndjson
```

NDJSON preserves message ID, correlation ID, content type, properties, and payload across brokers.
