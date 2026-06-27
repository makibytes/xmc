# Azure Service Bus (`azmc`)

Auth: `--connection-string` / `-s` (`AZMC_CONNECTION_STRING`) or `--namespace` (`AZMC_NAMESPACE`, Azure AD / `az login`). One of the two is required.

## Addressing

Bare queue/topic names — auto-created on first use (requires Manage claim on the connection string).

```
send myqueue "msg"
receive myqueue
publish notifications "alert"
subscribe notifications -g team-ops -n 0
subscribe notifications --subscription existing-sub -n 0   # target pre-existing subscription
```

## Subscriptions

- `-g <group>`: named subscription (competing consumers within the group; different groups = fan-out).
- `--subscription <name>`: directly target a specific pre-existing subscription (overrides `-g`).
- `-D`: durable (persists read position). No `-g`, no `-D`: ephemeral (deleted on close).

## Dead-letter queues

```
receive myqueue/$deadletterqueue -n 0       # peek dead-lettered messages
move myqueue/$deadletterqueue myqueue -n 0  # redrive
```

## Manage

`list` (topics show subscriptions as children; press `x` in AI shell to expand),
`purge <queue>` (drains by receive-and-delete — no native purge API),
`stats <queue>` (active + total message counts),
`create-queue <name>`,
`delete-queue <name>`,
`create-topic <name>`,
`delete-topic <name>` (also deletes all subscriptions).

## Constraints

- No subscription-level selectors (flag accepted but not applied as a filter rule).
