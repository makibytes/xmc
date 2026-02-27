#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "==> Running XMC integration tests"
echo "    Requires Docker to be running"
echo

brokers=(
    "artemis:artemis"
    "rabbitmq:rabbitmq"
    "kafka:kafka"
    "nats:nats"
    "mqtt:mqtt"
    "pulsar:pulsar"
)

failed=()

for entry in "${brokers[@]}"; do
    tag="${entry%%:*}"
    pkg="${entry##*:}"
    echo "--- Testing ${tag} ---"
    if ! (cd "${ROOT_DIR}" && go test -tags "${tag} integration" -timeout 120s -v "./broker/${pkg}/..." 2>&1); then
        failed+=("${tag}")
    fi
    echo
done

if (( ${#failed[@]} > 0 )); then
    echo "FAILED: ${failed[*]}"
    exit 1
fi

echo "All integration tests passed."
