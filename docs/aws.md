# Getting Started with AWS SQS + SNS (`awsmc`)

Binary: **`awsmc`** — AWS SQS/SNS Messaging Client

```bash
go build -tags aws -o awsmc .
```

## Authentication

`awsmc` uses the **standard AWS credential chain** — the same credentials the AWS CLI uses. No xmc-specific access key flags are needed.

```bash
export AWSMC_REGION="us-east-1"       # or use -s us-east-1
```

Authentication (pick one, in precedence order):

1. **Environment variables:** `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN`
2. **Shared credentials file:** `~/.aws/credentials` (default profile or `--profile`)
3. **IAM role:** instance profile / ECS task role / Lambda execution role

```bash
# Use a named profile
export AWSMC_PROFILE="my-dev-profile"
```

For LocalStack or other local endpoints:

```bash
export AWSMC_ENDPOINT="http://localhost:4566"
```

---

## 1. Send and Receive

SQS queues are auto-created on first use:

```bash
awsmc send myqueue "Hello World"
awsmc receive myqueue
```

Output:
```
Hello World
```

---

## 2. Admin Commands and SNS→SQS Fan-Out

### List, stats, and purge

```bash
awsmc manage list                  # lists all SQS queues and SNS topics
awsmc manage stats myqueue         # message counts (visible + in-flight)
awsmc manage purge myqueue         # remove all messages (rate-limited by AWS to once/60s)
```

### SNS→SQS fan-out

AWS topics use SNS (Simple Notification Service). When you subscribe to an SNS topic, `awsmc` automatically creates an SQS queue and wires an SNS subscription to it — messages published to the topic fan out to all subscriber queues.

Publish to a topic:

```bash
awsmc publish notifications "System alert"
```

Subscribe with different groups — each gets an independent SQS queue:

```bash
# Terminal 1
awsmc subscribe notifications -g ops-team -n 0

# Terminal 2
awsmc subscribe notifications -g dev-team -n 0
```

Both `ops-team` and `dev-team` receive every published message.

### Queue-based competing consumers

Multiple receivers on the same queue share the load:

```bash
# Terminal 1
awsmc receive tasks -w -n 0

# Terminal 2
awsmc receive tasks -w -n 0
```

Messages are distributed across both consumers.

**Available manage commands:** `list`, `purge`, `stats`.

---

## 3. Deep-Dive into xmc Features

### Request / Reply

Start a responder:

```bash
awsmc reply requests -e -n 0
```

Send a request and wait for the reply:

```bash
awsmc request requests "What is the queue depth?"
```

Process requests dynamically:

```bash
awsmc reply requests -x "date '+%H:%M:%S'" -n 0
```

### Message Properties

SQS supports message attributes (application properties):

```bash
awsmc send tasks "Deploy" -P env=prod -P priority=high
```

Receive with JSON output:

```bash
awsmc receive tasks -J
```

### Custom Output Format

```bash
awsmc receive tasks -F "%p{env}: %s\n"
```

### Move and Forward

Redrive messages from a dead-letter queue:

```bash
awsmc move dead-letters tasks -n 0
```

Continuous forwarding with transformation:

```bash
awsmc forward incoming processed -x "jq '.status = \"reviewed\"'"
```

### Durable Subscriptions

SNS→SQS subscriptions are inherently durable — messages are retained in the SQS queue until consumed:

```bash
awsmc subscribe events -D -g my-durable-sub -n 0
```

### Bulk and Streaming

```bash
seq 100 | awsmc send loadtest -l --rate 10
awsmc receive loadtest -n 0 --stats
awsmc subscribe events --for 30s --stats
```

---

## 4. Bridge to Another Broker

Stream from AWS to RabbitMQ using NDJSON:

```bash
# Drain an SQS queue into a RabbitMQ queue
awsmc receive myqueue -n 0 --ndjson | rmc send target --ndjson

# Continuous bridge from SNS topic to RabbitMQ
awsmc subscribe events -n 0 --ndjson | rmc send events --ndjson
```

NDJSON preserves message ID, correlation ID, content type, properties, and payload across brokers.
