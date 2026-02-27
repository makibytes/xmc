#!/bin/bash
set -euo pipefail

TIMEOUT=300s
PASS=0
FAIL=0
PIDS=()

run_broker() {
  local name="$1"; local tags="$2"; local pkg="$3"
  (
    echo "▶ $name..."
    if go test -tags "$tags" -timeout "$TIMEOUT" -count=1 -v "$pkg" > "/tmp/xmc-integration-$name.log" 2>&1; then
      echo "✅ $name passed"
    else
      echo "❌ $name FAILED (see /tmp/xmc-integration-$name.log)"
      exit 1
    fi
  ) &
  PIDS+=($!)
}

run_broker "artemis"  "artemis integration"  "./broker/artemis/"
run_broker "rabbitmq" "rabbitmq integration" "./broker/rabbitmq/"
run_broker "kafka"    "kafka integration"    "./broker/kafka/"
run_broker "nats"     "nats integration"     "./broker/nats/"
run_broker "mqtt"     "mqtt integration"     "./broker/mqtt/"
run_broker "pulsar"   "pulsar integration"   "./broker/pulsar/"

# IBM MQ requires CGO_ENABLED=1 and IBM MQ client SDK installed
if [ "${RUN_IBMMQ:-0}" = "1" ]; then
  run_broker "ibmmq" "ibmmq integration" "./broker/ibmmq/"
fi

for pid in "${PIDS[@]}"; do
  if wait "$pid"; then PASS=$((PASS+1)); else FAIL=$((FAIL+1)); fi
done

echo ""
echo "Results: $PASS passed, $FAIL failed"
[ "$FAIL" -eq 0 ]
