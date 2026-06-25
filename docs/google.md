# Google Cloud Pub/Sub (`gmc`)

Project: `--project` / `-s` or env `GMC_PROJECT`. Auth: Application Default Credentials (`gcloud auth application-default login`) or `--credentials <service-account.json>` (env `GMC_CREDENTIALS`). Emulator: `--endpoint` or env `GMC_SERVER`.

## Addressing

Topic and queue names are bare strings — used as Pub/Sub topic names directly. Topics and subscriptions are auto-created on first use.

- **Queue**: `send`/`receive` use a topic + auto-created subscription `xmc-queue-<name>` for competing consumers.
- **Topic**: `publish`/`subscribe` use the topic directly; subscription derived from `-g`/`-D`.

```
send myqueue "msg"
receive myqueue
publish events "msg"
subscribe events -g analytics -n 0     # named subscription "analytics"
subscribe events -g alerting -n 0      # independent subscription "alerting"
```

## Consumer groups

- `-g <group>`: named subscription (competing consumers within group; different groups = fan-out)
- `-D`: durable subscription (`xmc-durable-<topic>`)
- No `-g`, no `-D`: ephemeral subscription (auto-deleted on close)

## Manage commands

`list` only. Use `gcloud pubsub` or the GCP Console for topic/subscription lifecycle management.

## Supported features

- Application properties (`-P`, carried as Pub/Sub attributes)
- Correlation-id, reply-to, content-type, message-id
- Request/reply, move, forward
- Durable subscriptions

## Constraints

- No per-message TTL (retention is subscription-level, set in GCP Console)
- No selectors, no priority
- No manage create/delete/purge/stats — use `gcloud`
