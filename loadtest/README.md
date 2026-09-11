# Load and Scale Test Suite

This suite validates the memory service at realistic volume: it seeds a configurable dataset,
drives sustained HTTP load via Hyperfoil benchmarks, exhaustively walks every paginated endpoint
to check for skipped or duplicate entries, and aggregates all results into a Markdown/JSON report.
Results are written to `loadtest/results/` and are suitable for capture as CI artifacts.

---

## Prerequisites

- **`task dev:memory-service` must be running** on port **8082** before executing any load-test task.
- API key: `agent-api-key-1` (configured automatically by the dev stack via `MEMORY_SERVICE_API_KEYS_AGENT`).
- **Rate limiting must be disabled** for load testing — start the service with `MEMORY_SERVICE_RATE_LIMIT_MODE=off`, otherwise the token-bucket limiter will reject benchmark traffic.
- **`MEMORY_SERVICE_TRUSTED_USER_ID_CLIENTS=agent`** must be set so that `X-User-ID` headers sent by the generator and correctness tests are trusted by the server.
- [jbang](https://www.jbang.dev/download/) must be installed and on your `PATH` for Hyperfoil benchmarks (`loadtest:bench`).
- Go 1.21+ for the generator, correctness, and report binaries.

Start the dev stack with load-test overrides:

```sh
MEMORY_SERVICE_RATE_LIMIT_MODE=off \
MEMORY_SERVICE_TRUSTED_USER_ID_CLIENTS=agent \
task dev:memory-service
```

---

## Quick Start

Run the full pipeline in one command:

```sh
task loadtest:all
```

This executes **seed → bench → correctness → report** in sequence and writes all output to `loadtest/results/`.

---

## Individual Tasks

### `task loadtest:seed`

Seeds the running memory service with a configurable dataset. Writes
`loadtest/results/seed-manifest.json` which the benchmark and correctness tasks consume.

**Default seed:** 2000 conversations, 10 fork chains.

**You do not need to delete the manifest between runs.** Re-running `task loadtest:all`
is safe — the seed step skips automatically if the manifest already exists. Only delete it
in these specific situations:

| Situation | Action |
|---|---|
| Normal re-run (same DB) | Just run `task loadtest:all` — seed skips automatically |
| DB was wiped or restarted | `rm loadtest/results/seed-manifest.json && task loadtest:all` |
| Want different conversation count | `rm loadtest/results/seed-manifest.json && task loadtest:seed -- --total-conversations=2000 --fork-chains=40` |

```sh
# Default (2000 conversations, 10 fork chains)
task loadtest:seed

# Larger scale run (2000 conversations, 40 fork chains — ~2% fork rate)
task loadtest:seed -- --total-conversations=2000 --fork-chains=40

# All available flags:
go run ./internal/loadtest/generator/ --help
```

**Generator flags:**

| Flag | Default | Description |
|---|---|---|
| `--total-conversations` | `2000` | Number of main conversations to seed |
| `--fork-chains` | `10` | Number of fork chains (each = 1 root + 1 fork conversation) |
| `--worker-count` | `5` | Concurrent seeding workers |
| `--base-url` | `http://localhost:8082` | Memory service base URL |
| `--api-key` | `agent-api-key-1` | API key |
| `--seed-manifest-path` | `loadtest/results/seed-manifest.json` | Output manifest path |

> **Sign of a stale manifest:** correctness tests return HTTP 403 after a DB wipe.
> Fix: `rm loadtest/results/seed-manifest.json && task loadtest:all`

**Entry count distribution** (matches real-world chat patterns):

| Bucket | Probability | Turn pairs | Stored entries |
|---|---|---|---|
| Short | 60% | 2–10 | 4–20 |
| Medium | 30% | 11–100 | 22–200 |
| Long tail | 10% | 101–2000 | 202–4000 |

### `task loadtest:bench`

Runs all Hyperfoil benchmark flows **and** the SSE event-delivery latency
benchmark against the seeded service.  Writes per-flow JSON result files to
`loadtest/results/hyperfoil-*.json` and `loadtest/results/sse-event-delay-*.json`.

```sh
task loadtest:bench
```

Requires `loadtest:seed` to have completed first.

### `task loadtest:bench:sse-delay`

Runs **only** the SSE event-delivery latency benchmark — useful for iterating
on SSE performance without re-running the full Hyperfoil suite.

```sh
task loadtest:bench:sse-delay
```

**What it measures:** the time from a `POST /v1/conversations/{id}/entries`
completing until the matching `conversation/updated` SSE event arrives on the
user's open subscriber connection — the true end-to-end event delivery latency.

**Topology (matches Hiram's realistic fan-out model):**

| Role | Count per level |
|---|---|
| User sender connections | N (1 / 10 / 50) |
| User SSE subscriber connections | N (1 / 10 / 50, one per user) |
| Admin SSE subscribers (cognition) | 2 (persistent, whole run) |

Each append fans out to exactly 3 SSE connections: the owning user's subscriber
plus 2 admin subscribers.  Events for user A are never delivered to user B's
subscriber.

The benchmark runs three ramp levels sequentially (**1 → 10 → 50 concurrent
users**, 30 s each) and emits p50/p95/p99 latency per level.  The report table
shows all three as separate rows so degradation under fan-out pressure is
immediately visible.

### `task loadtest:correctness`

Runs the Go pagination correctness tests. Exhaustively walks every paginated
endpoint and asserts no entries are skipped or duplicated.

```sh
task loadtest:correctness
```

Requires `loadtest:seed` to have completed first.

### `task loadtest:report`

Reads all JSON result files from `loadtest/results/` and produces:
- `loadtest/results/report.md` — Markdown report with seed statistics, throughput, latency, and correctness
- `loadtest/results/report.json` — machine-readable version of the same data

The report includes a **Seed Data** section showing: total conversations, total entries, fork chains,
unique owners, min/max/avg/median entries per conversation, distribution by bucket (short/medium/long),
and participant type breakdown (single-user / two-user / two-agent).

```sh
task loadtest:report
```

The report binary exits 0 when all benchmarks and correctness checks pass.
It exits 1 when any SLO threshold is breached, any correctness check fails, or the output files cannot be written.
When result files are absent the corresponding section shows "not yet run" and those sections are excluded from the pass/fail determination.

---

## SLO Defaults

| Endpoint | p99 SLO |
|---|---|
| append-throughput | 500 ms |
| list-conversations | 300 ms |
| list-entries | 300 ms |
| search-conversations | 1000 ms |
| list-forks | 300 ms |
| sse-fan-out/burst-append | 500 ms |
| sse-event-delay/users-1 | N/A (observability) |
| sse-event-delay/users-10 | N/A (observability) |
| sse-event-delay/users-50 | N/A (observability) |

Override thresholds by editing the corresponding `loadtest/benchmarks/*.hf.yaml` file.

---

## Results

All output is written to `loadtest/results/` (gitignored except `.gitkeep`):

| File | Written by | Description |
|---|---|---|
| `seed-manifest.json` | `loadtest:seed` | All seeded conversation IDs, entry counts, fork chains |
| `conversation-ids.csv` | `loadtest:bench` (via manifest-to-csv helper) | Flat list consumed by Hyperfoil |
| `hyperfoil-<name>-<timestamp>.json` | `loadtest:bench` | Raw Hyperfoil result per flow |
| `sse-event-delay-<timestamp>.json` | `loadtest:bench` / `loadtest:bench:sse-delay` | SSE append→event latency at 1/10/50 concurrent users |
| `correctness-report.json` | `loadtest:correctness` | Per-test pass/fail and item counts |
| `report.md` | `loadtest:report` | Human-readable Markdown summary |
| `report.json` | `loadtest:report` | Machine-readable aggregate report |

---

## CI Artifact Capture

Add the following steps to your GitHub Actions workflow to run the suite and retain all results:

```yaml
- name: Run load tests
  run: task loadtest:all

- uses: actions/upload-artifact@v4
  with:
    name: loadtest-results
    path: loadtest/results/
    if-no-files-found: warn
```

---

## Directory Layout

```
loadtest/                          ← non-Go assets
├── README.md                      ← this file
├── benchmarks/
│   ├── shared/
│   │   └── http-config.hf.yaml   ← shared connection pool config
│   ├── append-throughput.hf.yaml
│   ├── list-conversations.hf.yaml
│   ├── list-entries.hf.yaml
│   ├── search-conversations.hf.yaml
│   ├── list-forks.hf.yaml
│   ├── sse-fan-out.hf.yaml        ← Hyperfoil SSE TTFB + burst-append
│   ├── manifest-to-csv.sh         ← shell wrapper for the Go CSV helper
│   └── run.sh                     ← runs all Hyperfoil flows + ssedelay
└── results/
    └── .gitkeep                   ← keeps dir in git; *.json and *.md are gitignored

internal/loadtest/                 ← all Go source
├── generator/                     ← data seeder binary
│   ├── main.go
│   ├── config.go
│   ├── distribution.go
│   └── seeder.go
├── correctness/                   ← pagination correctness tests
│   ├── correctness_test.go
│   └── reporter.go
├── hfrun/                         ← Hyperfoil non-interactive runner
│   └── main.go                    ← starts jbang, polls log, fetches /stats/total via REST
├── ssedelay/                      ← SSE event-delivery latency benchmark (Go-native)
│   └── main.go                    ← 1/10/50 concurrent users, append→event p50/p95/p99
└── report/                        ← results aggregator binary
    └── main.go
```
