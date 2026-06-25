# IBM MQ (`imc`)

Default: `ibmmq://localhost:1414/QM1?channel=SYSTEM.DEF.SVRCONN` (env `IMC_SERVER`). Auth: `-u`/`-p` or env `IMC_USER`/`IMC_PASSWORD`. Queue manager: `--qmgr`/`-m` or env `IMC_QUEUE_MANAGER`. Channel: `--channel`/`-c` or env `IMC_CHANNEL`.

## Addressing

Queue-only broker — no topic/publish/subscribe commands. Queue names are used verbatim (case-sensitive, typically UPPERCASE by convention).

```
send DEV.QUEUE.1 "msg"
receive DEV.QUEUE.1
peek DEV.QUEUE.1 -n 5
```

## Manage commands

None — use IBM MQ Explorer, `runmqsc`, or the web console for queue management.

## Supported features

- Application properties (`-P`, stored in `usr.*` message property folder)
- Selectors (`-S`): JMS-style property selection
- Priority (`-Y 0-9`): native MQ priority
- TTL (`-E`): MQMD Expiry field
- Persistent (`-d`): survives queue manager restart
- Correlation-id, reply-to, content-type, message-id
- Request/reply, move, forward, peek

## Constraints

- Queue-only: no topic commands (publish, subscribe)
- Queues must be pre-defined by an MQ administrator
- Build requires IBM MQ client libraries (use container build)
