#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT_DIR="${OUT_DIR:-${ROOT_DIR}/dist}"
STRICT_MATRIX="${STRICT_MATRIX:-0}"

TARGETS=(
  "linux/amd64"
  "linux/arm64"
  "darwin/arm64"
  "windows/amd64"
)

failures=()

build_pure_go_flavor() {
  local tag="$1"
  local bin_name="$2"
  local goos="$3"
  local goarch="$4"

  local target_dir="${OUT_DIR}/${goos}-${goarch}"
  mkdir -p "${target_dir}"

  local suffix=""
  if [[ "${goos}" == "windows" ]]; then
    suffix=".exe"
  fi

  (cd "${ROOT_DIR}" && GOOS="${goos}" GOARCH="${goarch}" CGO_ENABLED=0 go build -tags "${tag}" -o "${target_dir}/${bin_name}${suffix}" .)
}

build_ibm_flavor() {
  local goos="$1"
  local goarch="$2"

  local target_dir="${OUT_DIR}/${goos}-${goarch}"
  mkdir -p "${target_dir}"

  local suffix=""
  if [[ "${goos}" == "windows" ]]; then
    suffix=".exe"
  fi

  local output_path="dist/${goos}-${goarch}/imc${suffix}"

  if [[ "${goos}" == "linux" ]]; then
    if [[ "${goarch}" != "amd64" ]]; then
      failures+=("ibmmq ${goos}/${goarch}: IBM MQ Linux redistributable SDK is currently published as X64 only in the public feed")
      return
    fi

    if ! (cd "${ROOT_DIR}" && TARGET_OS="linux" TARGET_ARCH="${goarch}" IMC_OUTPUT="${output_path}" ./scripts/build-imc-in-container.sh); then
      failures+=("ibmmq ${goos}/${goarch}: containerized build failed")
    fi
    return
  fi

  if [[ "${goos}" == "darwin" && "${goarch}" == "arm64" ]]; then
    if [[ "$(uname -s)" != "Darwin" || "$(uname -m)" != "arm64" ]]; then
      failures+=("ibmmq ${goos}/${goarch}: requires native darwin/arm64 runner with IBM MQ SDK installed")
      return
    fi

    if ! (cd "${ROOT_DIR}" && GOOS="darwin" GOARCH="arm64" go build -tags ibmmq -o "${output_path}" .); then
      failures+=("ibmmq ${goos}/${goarch}: build failed (install IBM MQ Dev Toolkit so cmqc.h is available)")
    fi
    return
  fi

  if [[ "${goos}" == "windows" && "${goarch}" == "amd64" ]]; then
    if [[ "${OS:-}" != "Windows_NT" ]]; then
      failures+=("ibmmq ${goos}/${goarch}: requires native Windows x64 runner with IBM MQ SDK installed")
      return
    fi

    if ! (cd "${ROOT_DIR}" && GOOS="windows" GOARCH="amd64" go build -tags ibmmq -o "${output_path}" .); then
      failures+=("ibmmq ${goos}/${goarch}: build failed (ensure IBM MQ SDK headers/libs are installed)")
    fi
    return
  fi

  failures+=("ibmmq ${goos}/${goarch}: unsupported target")
}

for target in "${TARGETS[@]}"; do
  goos="${target%%/*}"
  goarch="${target##*/}"

  echo "==> Building ${goos}/${goarch}"

  build_pure_go_flavor "artemis" "amc" "${goos}" "${goarch}"
  build_pure_go_flavor "mqtt" "mmc" "${goos}" "${goarch}"
  build_pure_go_flavor "kafka" "kmc" "${goos}" "${goarch}"
  build_pure_go_flavor "rabbitmq" "rmc" "${goos}" "${goarch}"
  build_pure_go_flavor "nats" "nmc" "${goos}" "${goarch}"
  build_pure_go_flavor "pulsar" "pmc" "${goos}" "${goarch}"

  build_ibm_flavor "${goos}" "${goarch}"

done

if (( ${#failures[@]} > 0 )); then
  echo
  echo "Completed with IBM MQ platform constraints:"
  for failure in "${failures[@]}"; do
    echo "- ${failure}"
  done
  if [[ "${STRICT_MATRIX}" == "1" ]]; then
    exit 1
  fi
fi

echo

echo "All requested platform builds completed successfully. Artifacts are in ${OUT_DIR}."
