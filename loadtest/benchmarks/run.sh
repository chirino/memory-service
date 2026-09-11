#!/bin/sh
# run.sh — Run all Hyperfoil benchmark flows.
#
# Hyperfoil is started as a background server (start-local), the CLI connects
# to it for each command, and stats are collected via the REST API before
# shutdown.  This is the correct non-interactive pattern for Hyperfoil 0.27.2.
#
# Prerequisites:
#   - jbang installed and on PATH  (https://www.jbang.dev/download/)
#   - task dev:memory-service running on port 8082
#   - task loadtest:seed completed (loadtest/results/seed-manifest.json present)
#
# Usage (from repo root):
#   sh loadtest/benchmarks/run.sh
#
# POSIX-compatible — uses sh, not bash.
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
RESULTS_DIR="${REPO_ROOT}/loadtest/results"
BENCHMARKS_DIR="${REPO_ROOT}/loadtest/benchmarks"
TIMESTAMP="$(date +%Y%m%dT%H%M%S)"

# Hyperfoil version and jbang coordinates (pinned for reproducibility)
HF_VERSION="0.27.2"
HF_MAIN="io.hyperfoil.cli.HyperfoilCli"
HF_DEPS="io.hyperfoil:hyperfoil-core:${HF_VERSION},io.hyperfoil:hyperfoil-clustering:${HF_VERSION},io.hyperfoil:hyperfoil-http:${HF_VERSION}"
HF_CLI="io.hyperfoil:hyperfoil-cli:${HF_VERSION}"
HF_LOG="/tmp/hyperfoil/hyperfoil.local.log"

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

log() {
    printf '[run.sh] %s\n' "$*"
}

die() {
    printf '[run.sh] ERROR: %s\n' "$*" >&2
    exit 1
}

# hf_cli <cmd> — send a single command to the running controller.
# The controller address is stored in HF_CONTROLLER_ADDR.
hf_cli() {
    printf '%s\nexit\n' "$1" \
        | jbang --deps "${HF_DEPS}" --main "${HF_MAIN}" "${HF_CLI}" 2>/dev/null
}

# ---------------------------------------------------------------------------
# Prerequisite checks
# ---------------------------------------------------------------------------

command -v jbang >/dev/null 2>&1 || die "jbang not found on PATH. Install from https://www.jbang.dev/download/"

MANIFEST="${RESULTS_DIR}/seed-manifest.json"
if [ ! -f "${MANIFEST}" ]; then
    die "Seed manifest not found at ${MANIFEST}. Run 'task loadtest:seed' first."
fi

mkdir -p "${RESULTS_DIR}"

# ---------------------------------------------------------------------------
# Step 1: Generate CSV files from seed manifest
# ---------------------------------------------------------------------------

log "Generating CSV files from seed manifest..."
cd "${REPO_ROOT}"
sh "${BENCHMARKS_DIR}/manifest-to-csv.sh"
log "CSV generation complete."

# ---------------------------------------------------------------------------
# Step 2: Define flows to run
# ---------------------------------------------------------------------------

FLOWS="append-throughput list-conversations list-entries search-conversations list-forks"

# SSE end-to-end event delivery latency benchmark (Go-native, not Hyperfoil).
# Runs after the Hyperfoil flows so SSE connections don't interfere with
# the append-throughput measurement.
SSE_DELAY_OUT="${RESULTS_DIR}/sse-event-delay-${TIMESTAMP}.json"

# ---------------------------------------------------------------------------
# Step 3: Run each flow — start controller, run benchmark, fetch stats, stop
#
# Pattern (correct for Hyperfoil 0.27.2 non-interactive use):
#   a) Start controller in background, capture its port from the log
#   b) Upload + run + wait-run in ONE pipe (all before wait-run blocks)
#   c) Poll log for "Run XXX completed" to know when done
#   d) Fetch /run/XXX/stats/total via REST while controller is alive
#   e) Send shutdown via CLI, then kill background process
# ---------------------------------------------------------------------------

PASSED=""
FAILED=""

run_flow() {
    FLOW="$1"
    YAML="${BENCHMARKS_DIR}/${FLOW}.hf.yaml"
    RESULT_JSON="${RESULTS_DIR}/hyperfoil-${FLOW}-${TIMESTAMP}.json"

    if [ ! -f "${YAML}" ]; then
        log "WARNING: ${YAML} not found, skipping."
        FAILED="${FAILED} ${FLOW}(missing)"
        return
    fi

    log "Running flow: ${FLOW} ..."

    # Clear the Hyperfoil log so we start fresh.
    rm -f "${HF_LOG}"

    # Start the CLI with start-local + upload + run + wait-run in one pipe.
    # After wait-run the CLI blocks waiting for more stdin — we DON'T send
    # exit yet so the controller stays alive for the REST API call.
    # We use the Go hfrun helper which handles this correctly.
    if go run "${REPO_ROOT}/internal/loadtest/hfrun/" \
            --yaml="${YAML}" \
            --out="${RESULT_JSON}"; then
        log "PASS: ${FLOW} — results written to ${RESULT_JSON}"
        PASSED="${PASSED} ${FLOW}"
    else
        log "FAIL: ${FLOW} — see ${RESULT_JSON}"
        FAILED="${FAILED} ${FLOW}"
    fi
}

for FLOW in ${FLOWS}; do
    run_flow "${FLOW}"
done

# ---------------------------------------------------------------------------
# Step 3b: Run SSE event-delay benchmark
# ---------------------------------------------------------------------------

log "Running SSE event-delay benchmark (1/10/50 concurrent users)..."
if go run "${REPO_ROOT}/internal/loadtest/ssedelay/" \
        --base-url="http://localhost:8082" \
        --api-key="agent-api-key-1" \
        --admin-api-key="admin-api-key-1" \
        --duration=30 \
        --out="${SSE_DELAY_OUT}"; then
    log "PASS: sse-event-delay — results written to ${SSE_DELAY_OUT}"
    PASSED="${PASSED} sse-event-delay"
else
    log "FAIL: sse-event-delay — see ${SSE_DELAY_OUT}"
    FAILED="${FAILED} sse-event-delay"
fi

# ---------------------------------------------------------------------------
# Step 4: Print summary
# ---------------------------------------------------------------------------

printf '\n'
log "========================================"
log "Benchmark run complete — ${TIMESTAMP}"
log "========================================"
if [ -n "${PASSED}" ]; then
    log "PASSED:${PASSED}"
fi
if [ -n "${FAILED}" ]; then
    log "FAILED:${FAILED}"
fi
printf '\n'

if [ -n "${FAILED}" ]; then
    log "One or more flows failed. See result files in ${RESULTS_DIR}/"
    exit 1
fi

log "All flows passed."
exit 0
