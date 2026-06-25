# AWS SQS + SNS (`awsmc`)

Region: `-s us-east-1` or env `AWSMC_REGION` (default: `us-east-1`). Auth: standard AWS credential chain (env vars / `~/.aws/credentials` / IAM role). Profile: `--profile` or env `AWSMC_PROFILE`. LocalStack: `--endpoint` or env `AWSMC_ENDPOINT`.

## Addressing

- **Queue**: SQS queue names (auto-created on first use, idempotent).
- **Topic**: SNS topic names. Subscribe auto-creates an SQS queue subscribed to the SNS topic (SNS→SQS fan-out with raw delivery).

```
send myqueue "msg"
receive myqueue
publish notifications "alert"
subscribe notifications -g ops-team -n 0     # SQS queue "ops-team" subscribed to SNS topic
subscribe notifications -g dev-team -n 0     # independent subscriber queue
```

## Consumer groups

- Queue: multiple `receive` on the same queue = competing consumers (SQS distributes)
- Topic: `-g <group>` creates a named SQS subscriber queue; same group = competing; different groups = independent fan-out
- Durable: `-D -g <name>` creates a persistent subscriber queue

## Manage commands

`list`, `purge <queue>` (rate-limited by AWS to once/60s), `stats <queue>` (visible + in-flight counts).

## Supported features

- Application properties (`-P`, carried as SQS/SNS MessageAttributes)
- Correlation-id, reply-to, content-type, message-id
- Request/reply, move, forward

## Constraints

- No per-message TTL (SQS retention is queue-level)
- No selectors, no priority
- Queue names: alphanumeric, hyphens, underscores only (no dots)
- FIFO queues not yet supported
- Ephemeral subscriber queues are cleaned up on close; durable ones persist
