#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

DOCKER_BIN="${DOCKER_BIN:-docker}"
BUILDER_IMAGE="${IMC_BUILDER_IMAGE:-xmc-ibmmq-builder:latest}"
TARGET_OS="${TARGET_OS:-linux}"
TARGET_ARCH="${TARGET_ARCH:-amd64}"
IMC_OUTPUT="${IMC_OUTPUT:-imc}"
MQ_VRMF="${IBM_MQ_VRMF:-9.4.5.0}"

if [[ "${TARGET_OS}" != "linux" ]]; then
  echo "Containerized IBM build supports TARGET_OS=linux only (received: ${TARGET_OS})" >&2
  exit 2
fi

if [[ -z "${IMC_BUILDER_PLATFORM:-}" ]]; then
  BUILDER_PLATFORM="linux/${TARGET_ARCH}"
else
  BUILDER_PLATFORM="${IMC_BUILDER_PLATFORM}"
fi

if [[ -z "${IBM_MQ_ARCH:-}" ]]; then
  case "${TARGET_ARCH}" in
    amd64) MQ_ARCH="X64" ;;
    arm64) MQ_ARCH="ARM64" ;;
    *)
      echo "Unsupported TARGET_ARCH for IBM MQ redistributable: ${TARGET_ARCH}" >&2
      exit 2
      ;;
  esac
else
  MQ_ARCH="${IBM_MQ_ARCH}"
fi

"${DOCKER_BIN}" build \
  --platform "${BUILDER_PLATFORM}" \
  --build-arg "MQ_VRMF=${MQ_VRMF}" \
  --build-arg "MQ_ARCH=${MQ_ARCH}" \
  -f "${ROOT_DIR}/build/ibmmq/Dockerfile" \
  -t "${BUILDER_IMAGE}" \
  "${ROOT_DIR}"

"${DOCKER_BIN}" run --rm \
  --platform "${BUILDER_PLATFORM}" \
  -v "${ROOT_DIR}:/workspace" \
  -w /workspace \
  -e "IMC_OUTPUT=${IMC_OUTPUT}" \
  "${BUILDER_IMAGE}" \
  bash -c 'mkdir -p "$(dirname "${IMC_OUTPUT}")" && go build -tags ibmmq -buildvcs=false -o "${IMC_OUTPUT}" .'
