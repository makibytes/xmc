# Google Cloud Pub/Sub (`gmc`)

Project: `--project` / `-s` / `GMC_PROJECT`. Auth: Application Default Credentials (`gcloud auth application-default login`) or `--credentials <svc-account.json>` / `GMC_CREDENTIALS`. Emulator: `--endpoint` / `GMC_SERVER`.

## Addressing

Bare names — topics and subscriptions auto-created on first use.

- **Queue**: `send`/`receive` use a topic + fixed subscription `xmc-queue-<name>` for competing consumers.
- **Topic**: `publish`/`subscribe` use the topic directly; subscription derived from `-g`/`-D`.

```
send myqueue "msg"
receive myqueue
publish events "msg"
subscribe events -g analytics -n 0
subscribe events --subscription existing-sub -n 0   # target pre-existing subscription
```

## Consumer groups

`-g <group>`: named subscription; same group = competing, different groups = fan-out.
`-D`: durable (`xmc-durable-<topic>`). No `-g`, no `-D`: ephemeral (deleted on close).
`--subscription <name>`: override to target a specific pre-existing subscription (overrides `-g`).

## Manage

`list` (topics show subscriptions as children; press `x` in AI shell to expand; Queues window shows `xmc-queue-*` subscriptions by logical name),
`purge <queue>` (seeks the `xmc-queue-<name>` subscription to now — drops backlog),
`create-queue <name>` (creates topic + `xmc-queue-<name>` subscription),
`delete-queue <name>` (deletes subscription and backing topic),
`create-topic <name>`,
`delete-topic <name>`.

## Constraints

- No per-message TTL (retention is subscription-level, set in GCP Console).
- No selectors, no priority.
- `manage stats` is not available (backlog count requires the Cloud Monitoring API — use GCP Console).
- Queue emulation: each queue is a Pub/Sub topic with a single shared pull subscription.
