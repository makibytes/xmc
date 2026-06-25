# Azure Service Bus (`azmc`)

Auth: `--connection-string` / `-s` (env `AZMC_CONNECTION_STRING`) or `--namespace` (env `AZMC_NAMESPACE`, uses Azure AD / `az login`). One of the two is required.

## Addressing

Queue and topic names are flat strings — used directly as Service Bus entity names. Entities are auto-created if they don't exist (requires Manage claim on the connection string).

```
send myqueue "msg"
receive myqueue
publish notifications "alert"
subscribe notifications -g team-ops -n 0
```

## Subscriptions

- `-g <group>`: named subscription (competing consumers within the group; different groups = fan-out)
- `-D`: durable subscription (persists read position)
- Default `-g xmc-consumer-group` creates a persistent subscription on the topic
- Ephemeral subscriptions (no `-g`, no `-D`) are auto-deleted on close

## Dead-letter queues

Access via `/$deadletterqueue` suffix:

```
receive myqueue/$deadletterqueue -n 0     # peek at dead-lettered messages
move myqueue/$deadletterqueue myqueue -n 0  # redrive
```

## Manage commands

`list`, `purge <queue>` (drains by receiving — no native purge API), `stats <queue>` (active + total counts).

## Supported features

- Application properties (`-P`), message-id, correlation-id, reply-to, content-type
- Per-message TTL (`-E`): native `TimeToLive` field (first-class, enforced by the broker)
- Priority (`-Y`), persistent (`-d`)
- Native peek (non-destructive at the API level)
- Request/reply, move, forward
- Durable subscriptions

## Constraints

- No selectors on subscriptions (selector flag is accepted but not applied as a filter rule)
- Entities must be pre-created or xmc auto-creates them (needs Manage claim)
