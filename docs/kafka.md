# Apache Kafka (`kmc`)

Default: `kafka://localhost:9092` (env `KMC_SERVER`). Auth: `-u`/`-p` (SASL PLAIN). TLS: `kafka+ssl://` or `--tls`. Multiple brokers: `kafka://b1:9092,b2:9092`.

## Addressing

Topic-only broker — no queue commands (send/receive/peek/request/reply/move unavailable). Topic names are bare strings, no transformation.

```
publish mytopic "msg"
subscribe mytopic
publish mytopic -K "user-42" "msg"    # -K sets the partition key
```

## Consumer groups

`-g <group>` (default: `xmc-consumer-group`). Kafka manages offsets per group — restart with the same group to resume. Multiple consumers in the same group share partitions.

```
subscribe orders -g processors -n 0
```

## Topic forward

Kafka supports topic-to-topic forwarding:

```
forward source-topic dest-topic
forward raw clean -x "jq '.status = \"done\"'"
```

## Manage commands

`list`, `create-topic <name> --partitions N --replication-factor N --config key=value`, `delete-topic <name>`.

## Supported features

- Application properties (`-P`, carried as Kafka headers), content-type, correlation-id, message-id
- Message key (`-K`) for partition affinity
- TTL (`-E`): stamps a `ttl` header (advisory only — Kafka uses topic-level `retention.ms` for actual expiry)

## Constraints

- Topic-only: no queue commands
- No selectors, no priority, no persistent flag (Kafka always persists)
- Topics auto-created on publish by default
