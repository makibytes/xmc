#!/usr/bin/env bash

# Auto-detect the broker binary or use XMC_BINARY if set
if [ -z "$XMC_BINARY" ]; then
    for bin in amc imc kmc mmc rmc; do
        if [ -f "./$bin" ]; then
            XMC_BINARY="./$bin"
            break
        fi
    done
fi

if [ -z "$XMC_BINARY" ]; then
    echo "No broker binary found. Build one first, e.g.: go build -tags artemis -o amc ."
    exit 1
fi

# Determine env var prefix from binary name
case "$(basename "$XMC_BINARY")" in
    amc) XMC_ENV_PREFIX="AMC" ;;
    imc) XMC_ENV_PREFIX="IMC" ;;
    kmc) XMC_ENV_PREFIX="KMC" ;;
    mmc) XMC_ENV_PREFIX="MMC" ;;
    rmc) XMC_ENV_PREFIX="RMC" ;;
    *)   echo "Unknown binary: $XMC_BINARY"; exit 1 ;;
esac

export XMC_BINARY XMC_ENV_PREFIX
echo "Running tests with $XMC_BINARY (env prefix: ${XMC_ENV_PREFIX}_)"
./test/bats/bin/bats -t test
