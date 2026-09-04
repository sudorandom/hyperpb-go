#!/usr/bin/env bash
#
# Long-running randomized differential testing for hyperjson; intended for
# unattended (e.g. weekend) runs. Interrupt with ctrl-C.
#
# This drives TestStressDifferential, a structure-aware corpus mutator that
# checks the direct arena writer, the JSON-to-wire transcoder, a protojson
# oracle, and marshal round-trips against each other. Native `go test -fuzz`
# targets also exist (fuzz_test.go) but are currently unbuildable: coverage
# instrumentation overflows the //go:nosplit stack budget in hyperpb's parser
# VM (an upstream issue; even hyperpb's own fuzz targets are affected).
#
# Failures do NOT stop the run: each failing chunk's log (containing the
# input and PRNG seed) is archived under fuzz-logs/failures/ and the loop
# continues with a fresh seed. Reproduce a finding with:
#   HYPERJSON_SEED=<seed> HYPERJSON_STRESS=15m go test -timeout 0 \
#       ./hyperjson/ -run TestStressDifferential -v
#
# Usage: ./fuzz.sh [chunk-duration]   (default 30m per chunk)

set -euo pipefail

CHUNK="${1:-30m}"
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LOG_DIR="${DIR}/fuzz-logs"
FAIL_DIR="${LOG_DIR}/failures"
mkdir -p "${FAIL_DIR}"

echo "stress-testing in ${CHUNK} chunks; logs in ${LOG_DIR}"

cycle=0
failures=0
while true; do
  cycle=$((cycle + 1))
  log="${LOG_DIR}/stress-cycle-${cycle}.log"
  echo "[cycle ${cycle}] $(date '+%F %T') running for ${CHUNK} (failures so far: ${failures})..."
  if (cd "${DIR}/.." && HYPERJSON_STRESS="${CHUNK}" \
      go test ./hyperjson/ -run TestStressDifferential -v -count=1 -timeout 0 >"${log}" 2>&1); then
    grep -h "ran .* iterations" "${log}" || true
    rm -f "${log}"
  else
    failures=$((failures + 1))
    mv "${log}" "${FAIL_DIR}/failure-${failures}.log"
    echo "[cycle ${cycle}] FAILURE #${failures} - archived to ${FAIL_DIR}/failure-${failures}.log; continuing with a fresh seed"
    grep -m1 -A6 "iteration" "${FAIL_DIR}/failure-${failures}.log" || true
  fi
done
