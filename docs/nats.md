# NATS / JetStream (`nmc`)

Default: `nats://localhost:4222` (env `NMC_SERVER`). Auth: `-u`/`-p` or env `NMC_USER`/`NMC_PASSWORD`. TLS: `--tls`. Requires JetStream enabled for queue operations.

## Addressing

- **Queue** commands use JetStream streams. Bare queue names are used directly — xmc auto-creates a WorkQueue-retention stream internally. No name transformation visible to the user.
- **Topic** commands use core NATS subjects. Subject names are bare strings, no transformation.

```
send myqueue "msg"              # JetStream stream (persistent, competing consumers)
receive myqueue
publish events.order "msg"      # core NATS subject (fire-and-forget)
subscribe events.>              # NATS wildcard: > matches any remaining tokens
```

## NATS subject wildcards

- `*` matches one token: `events.*` matches `events.order` but not `events.order.new`
- `>` matches one or more tokens: `events.>` matches `events.order` and `events.order.new`

## Manage commands

`list`, `create-queue <name> --retention workqueue|limits|interest --max-msgs N --subject <subj>`, `delete-queue <name>`.

## Supported features

- Request/reply (native: private per-request reply queue, auto-created and deleted)
- Peek browses stored messages by stream sequence (non-destructive, `-n 0` walks all)
- Move, forward
- Durable subscriptions via `-D -g <group>`
- Persistent delivery (`-d`) maps to JetStream

## Constraints

- No application properties (`-P`) or selectors (`-S`) at the protocol level
- Core NATS topics have no persistence — subscriber must be running before publish
- JetStream queue names are case-sensitive
