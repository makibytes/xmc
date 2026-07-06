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

- Application properties (`-P`) and metadata (correlation-id, reply-to, content-type, message-id) carried as NATS headers
- Request/reply (native: private per-request reply queue, auto-created and deleted)
- Peek browses stored messages by stream sequence (non-destructive, `-n 0` walks all)
- Move, forward
- `-g <group>` on subscribe maps to a core NATS queue group (competing consumers)

## Constraints

- No selectors (`-S`), no TTL, no priority; `-d` is rejected (queues are always JetStream-persistent, core-NATS topics never are)
- `--message-id` is carried as a plain header, NOT as `Nats-Msg-Id` — so repeat sends with the same ID are all stored (no JetStream dedup surprise)
- Core NATS topics have no persistence — subscriber must be running before publish; `-D` has no effect there
- JetStream queue names are case-sensitive
