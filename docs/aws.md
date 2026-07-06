# AWS SQS + SNS (`awsmc`)

Region: `-s us-east-1` or `AWSMC_REGION` (default: `us-east-1`). Auth: AWS credential chain (env vars / `~/.aws/credentials` / IAM role). Profile: `--profile` / `AWSMC_PROFILE`. LocalStack: `--endpoint` / `AWSMC_ENDPOINT`.

## Addressing

Bare names — SQS queues and SNS topics are auto-created on first use.

```
send myqueue "msg"
receive myqueue --visibility-timeout 60
publish notifications "alert"
subscribe notifications -g ops-team -n 0     # dedicated SQS queue subscribed to SNS topic
subscribe notifications -g dev-team -n 0     # independent subscriber queue (fan-out)
```

## FIFO queues

Name a queue with a `.fifo` suffix, or pass `--fifo` on send (appends the suffix automatically):

```
send orders.fifo "msg" --message-group-id checkout
send orders "msg" --fifo --message-group-id checkout   # auto-appends .fifo
```

`--message-group-id` is required for FIFO sends; without it, `-K <key>` is used, then the default group `xmc`. `--dedup-id` enables explicit deduplication; omit it for content-based deduplication (enabled by default). The group ID maps back to the key field on receive.

## Consumer groups (topics)

`-g <group>`: creates SQS subscriber queue `<group>-<topic>` (scoped by topic — SQS queue names are account-global); same group = competing consumers, different groups = fan-out. Group and durable subscriber queues stay SNS-subscribed after exit, so they keep buffering; only ephemeral ones (no `-g`) are unsubscribed and deleted on close.

## Manage

`list` (topics show subscriptions as children; press `x` in AI shell to expand),
`purge <queue>` (AWS rate-limits to once per 60 s per queue),
`stats <queue>` (visible + in-flight counts),
`create-queue <name>` (use `.fifo` suffix for FIFO queues),
`delete-queue <name>`,
`create-topic <name>`,
`delete-topic <name>`.

## Constraints

- `--visibility-timeout <sec>` (default 30): redelivery window for unacked messages (consume only).
- No per-message TTL (SQS retention is queue-level, set in AWS Console).
- No selectors, no priority.
- `-K` only maps to `MessageGroupId` on FIFO queues/topics; dropped on standard ones.
- Without `-I`, received messages get the SQS-assigned message ID as message-id.
- Queue names: alphanumeric, hyphens, underscores (and `.fifo` suffix for FIFO).
- Ephemeral subscriber queues cleaned up on close; durable ones persist.
