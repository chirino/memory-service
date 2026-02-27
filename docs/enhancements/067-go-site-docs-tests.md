---
status: partial
---

# Enhancement 067: Go-Based Site Documentation Tests

> **Status**: Partial — core implementation complete; end-to-end validation against real checkpoint apps pending.

## Summary

Port the Maven-based `site-tests` module's test execution pipeline to Go under
`internal/sitebdd/`, reusing the existing `godog` infrastructure from `internal/bdd/`.
The Astro/npm build phase that generates `test-scenarios.json` is retained unchanged;
only the Java test runner and step definitions are replaced with Go equivalents.
Tests run in parallel with one goroutine per scenario, and each scenario gets a
unique user suffix even when multiple scenarios share the same logical user name
(e.g. `alice`). The suite is **opt-in only** — excluded from regular `go test ./...`
runs via a build tag.

## Motivation

The current `site-tests` module requires Maven, Java 21, and a JVM build to run
documentation tests. This creates friction when the rest of the codebase is being
ported to Go:

- Two separate test runtimes to install and maintain (Java + Go).
- The Java Cucumber runner can't easily reuse step definitions from `internal/bdd/`
  (e.g. JSON assertion helpers, variable resolution, HTTP session management).
- Adding new step types for Go-specific test features would require duplicating
  logic across languages.
- The Maven `generate-test-resources` phase is slow (npm install + Astro build +
  TestGeneratorMain), most of which is still needed but the Java-specific parts
  are unnecessary overhead.

Replacing the Java runner with Go unifies the test toolchain and allows reuse of
`internal/testutil/cucumber` utilities.

## Non-Goals

- Removing or modifying the Astro/npm build pipeline that produces `test-scenarios.json`.
- Replacing the WireMock fixture file format — existing `site-tests/openai-mock/fixtures/`
  JSON files are reused as-is; new recordings write the same format.
- Porting the Java `site-tests` Maven module itself (it remains as-is; the Go suite
  runs in parallel with or instead of it).
- OIDC / Keycloak authentication for checkpoint apps — simple bearer-token auth
  is sufficient (the memory service runs in `config.ModeTesting`).

## Design

### Architecture Overview

```
site/src/pages/docs/**/*.mdx
        │  (Astro build — unchanged)
        ▼
site-tests/target/generated-test-resources/test-scenarios.json
        │  (read by Go test at startup)
        ▼
internal/sitebdd/
  ├── site_test.go          ← TestMain + TestSiteDocs runner (build tag: site_tests)
  ├── scenarios.go          ← JSON loading + .feature file generation to tempdir
  ├── steps_curl.go         ← curl parsing, HTTP execution, variable substitution
  ├── steps_checkpoint.go   ← build + lifecycle of checkpoint subprocess per scenario
  ├── steps_openaimock.go   ← in-process OpenAI mock server (replaces WireMock)
  └── FACTS.md
```

The Go runner:
1. Reads `test-scenarios.json` at startup.
2. Generates `.feature` files into a temp directory (one feature file per source MDX page).
3. Starts a shared in-process memory service via `serve.StartServer()` (port 0).
4. Starts an in-process OpenAI mock HTTP server on a dynamic port.
5. Hands the temp feature dir to `godog` with `Concurrency: runtime.NumCPU()`.
6. Each scenario concurrently: allocates a unique port, builds and starts the
   checkpoint subprocess, runs curl steps, stops the checkpoint.

### Opting In (Not Running by Default)

The test file carries a build tag so it is invisible to `go test ./...`:

```go
//go:build site_tests
```

Run with:
```bash
go test -tags=site_tests ./internal/sitebdd/ -v -count=1
```

Tag-specific runs:
```bash
go test -tags=site_tests ./internal/sitebdd/ -v -run TestSiteDocs \
  -godog.tags=@quarkus
```

A `SITE_TESTS_JSON` environment variable overrides the default path to
`test-scenarios.json` (useful for CI where the Astro build runs in a different
working directory):
```bash
SITE_TESTS_JSON=/path/to/test-scenarios.json \
  go test -tags=site_tests ./internal/sitebdd/ -v
```

### test-scenarios.json Schema (Go structs)

```go
// scenarios.go
type ScenarioData struct {
    Checkpoint string          `json:"checkpoint"`
    SourceFile string          `json:"sourceFile"`
    Scenarios  []ScenarioCmd   `json:"scenarios"`
}

type ScenarioCmd struct {
    Bash         string        `json:"bash"`
    Expectations []Expectation `json:"expectations"`
    CustomSteps  []string      `json:"customSteps"`
}

type Expectation struct {
    Type  string `json:"type"`  // "contains", "not_contains", "status_code"
    Value string `json:"value"`
}
```

### Feature File Generation

`generateFeatureFiles(scenarios []ScenarioData, dir string) error` writes one
`.feature` file per unique source MDX file. Curl blocks keep the canonical
`localhost:9090` port from the docs — port substitution happens at runtime in
`steps_curl.go`. User names (`alice`, `bob`, `charlie`) are kept canonical in the
feature file for readability; the step definitions inject scenario-specific
suffixes at runtime.

Generated feature example:
```gherkin
# Generated from test-scenarios.json — DO NOT EDIT
Feature: Quarkus Getting Started Tutorial

  Scenario: [quarkus] Getting Started Tutorial - checkpoint-01
    # From /docs/quarkus/getting-started
    Given checkpoint "quarkus/examples/checkpoint-01" is active
    When I execute curl command:
      """
      curl -s -X POST http://localhost:9090/v1/conversations \
        -H "Authorization: Bearer alice" \
        -H "Content-Type: application/json" \
        -d '{"title": "My Conversation"}'
      """
    Then the response status should be 200
    And the response should contain "id"
    When I stop the checkpoint
```

Tags are generated from the source framework and checkpoint path — identical to
the Java `TestGenerator.deriveTags()` logic:
- `@quarkus`, `@spring`, or `@python` from `sourceFile` prefix
- `@checkpoint_quarkus_examples_checkpoint_01` from `checkpoint`

### Parallel Execution and Port Allocation

```go
// site_test.go
var nextPort atomic.Int32 // initialized to 10089

func allocatePort() int {
    return 10090 + int(nextPort.Add(1)) - 1
}
```

Each scenario receives a unique port in `Given checkpoint "..." is active`. The
`steps_checkpoint.go` stores it in the per-scenario `SiteScenario` struct.

godog concurrency:
```go
opts := godog.Options{
    Concurrency: runtime.NumCPU(),
    Randomize:   time.Now().UnixNano(),
}
```

### User Isolation

Every scenario gets a `scenarioUID` — a short UUID suffix (first 8 hex chars).

`steps_curl.go` applies two rewrites before executing each curl command:

1. **Port substitution**: `localhost:9090` → `localhost:<checkpointPort>`
2. **User substitution** in `Authorization` headers and JSON `"userId"` fields:
   - `Authorization: Bearer alice` → `Authorization: Bearer alice-<uid>`
   - `"userId": "alice"` → `"userId": "alice-<uid>"`
   - Applies to `alice`, `bob`, and `charlie`

Response normalization before assertions:
- `alice-<uid>` → `alice` (and for `bob`, `charlie`)

This mirrors the Java `CurlSteps.rewriteScenarioUserIdsInJson()` and
`normalizeScenarioUserIds()` pattern, with the uid replacing the port number.

### Per-Scenario State (SiteScenario struct)

```go
type SiteScenario struct {
    ScenarioUID     string          // short UUID suffix for user isolation
    CheckpointID    string          // e.g. "quarkus/examples/checkpoint-01"
    CheckpointPort  int             // dynamically allocated
    CheckpointProc  *os.Process     // subprocess handle
    LastRespBody    string          // last HTTP response body (normalized)
    LastStatusCode  int
    ContextVars     map[string]any  // named variables (set/get steps)
    MemServiceURL   string          // shared memory service base URL
    OpenAIURL       string          // shared mock OpenAI base URL
}
```

The `SiteScenario` is created fresh per scenario (not shared across concurrent
scenarios). No mutex is needed for per-scenario fields.

### Step Definitions

#### steps_curl.go

| Step | Description |
|------|-------------|
| `When I execute curl command:` | Parse curl block, apply substitutions, execute as Go HTTP request, store response |
| `Then the response status should be {int}` | Assert `LastStatusCode` |
| `Then the response should contain {string}` | Assert `LastRespBody` contains string |
| `Then the response should not contain {string}` | Inverse assertion |
| `Then the response should be json with items array` | Assert response has `data` array |
| `Then the response body should be json:` | Subset JSON assertion (reuses `TestScenario.JSONMustContain`) |
| `Then the response body should be text:` | Normalized text contains assertion |
| `Then set {string} to the json response field {string}` | Extract field into `ContextVars` |
| `Then the response should match pattern {string}` | Regex match on `LastRespBody` |

Curl command parsing converts each `curl` invocation in the bash block to a Go
`http.Request`. Supports:
- `-X METHOD`, `-H "Header: Value"`, `-d 'body'` / `--data`, `--data-binary`
- URL with `${VAR}` substitution from `ContextVars`
- Multi-curl blocks (sequential, last response is kept)
- Retry on 404/500/503 (up to 5 attempts, 3s delay) for startup transients
- `echo "..." > /tmp/file` setup commands executed via `exec.Command("bash", "-c", line)`

#### steps_checkpoint.go

| Step | Description |
|------|-------------|
| `Given checkpoint {string} is active` | Set checkpoint path, allocate unique port and scenarioUID |
| `When I build the checkpoint` | Build via `./mvnw` (Java) or `uv sync` (Python) |
| `When I build the checkpoint with {string}` | Custom build command |
| `Then the build should succeed` | Assert exit code == 0 |
| `When I start the checkpoint on port {int}` | Start subprocess, load fixtures, wait for port |
| `When I start the checkpoint` | Start on the pre-allocated dynamic port |
| `Then the application should be running` | Assert process alive |
| `When I stop the checkpoint` | Kill subprocess, reset WireMock scenario state |

Subprocess detection (same logic as Java):
- `pyproject.toml` exists → Python (`uvicorn`)
- `target/quarkus-app/quarkus-run.jar` exists → Quarkus
- Other JAR in `target/` → Spring Boot

Environment variables injected into checkpoint subprocess:
```
OPENAI_BASE_URL=<mockOpenAIURL>
OPENAI_API_KEY=not-needed
OPENAI_MODEL=mock-gpt-markdown
MEMORY_SERVICE_URL=<memServiceURL>
```

Port wait: poll TCP connect every 1s up to 90s, then 10s grace period for full
context initialization (matches Java behavior).

Cleanup: `t.Cleanup` + `ctx.After` hook both call `killProcess()` so cleanup runs
even on test panic or timeout.

#### steps_openaimock.go

Replaces WireMock. An in-process `net/http/httptest.Server` with a single handler
that dispatches by `checkpointID` stored in a per-scenario registry.

**Playback mode** (default):

1. `GET /v1/models` → reads `site-tests/openai-mock/mappings/models.json`,
   returns the `response.body` value.
2. `POST /v1/chat/completions`:
   - Looks up the calling scenario's `checkpointID` (from an `X-Scenario-ID`
     request header injected by `steps_checkpoint.go` into each checkpoint's env
     as `OPENAI_SCENARIO_ID`).
   - Reads the next fixture file:
     `site-tests/openai-mock/fixtures/<framework>/<checkpointID>/NNN.json`
     and increments the per-scenario counter.
   - If no fixture exists or counter is exhausted → generic fallback response.
3. Streaming (`"stream": true`) → synthesize SSE from the non-streaming fixture
   body (split into word-level chunks), matching the fixture's model/id.

Per-scenario state (fixture counter) is in `SiteScenario`, not shared.

**Recording mode** (`SITE_TEST_RECORD=true` or `SITE_TEST_RECORD=all`):

The mock server acts as a transparent reverse proxy to the real OpenAI API and
simultaneously captures each `/v1/chat/completions` response into an in-memory
journal per scenario.

```
env var              behaviour
─────────────────────────────────────────────────────────────────────
SITE_TEST_RECORD=    playback (default)
SITE_TEST_RECORD=true   record checkpoints with no existing fixtures;
                        play back checkpoints that already have fixtures
SITE_TEST_RECORD=all    record (overwrite) all checkpoints
OPENAI_API_KEY=<key>    required when recording; forwarded to real API
OPENAI_MODEL=<model>    optional, defaults to gpt-4o
```

Recording flow per scenario:

1. `steps_checkpoint.go` checks `hasFixtures(checkpointID)` and
   `SITE_TEST_RECORD` to decide record vs. playback.
2. If recording: sets `SiteScenario.Recording = true`; checkpoint subprocess
   gets `OPENAI_API_KEY` from env and `OPENAI_BASE_URL` pointing to the mock.
3. Mock handler (record path): forwards the request to `api.openai.com`
   (or `OPENAI_API_BASE` if set), captures the response body, appends to
   `SiteScenario.Journal []capturedCall`.
4. After `When I stop the checkpoint`: if `SiteScenario.Recording`,
   call `saveJournal(checkpointID, journal)`:
   - Clears any existing `NNN.json` files in the fixture dir.
   - For each captured `/v1/chat/completions` response, writes a WireMock-
     compatible stub JSON `001.json`, `002.json`, … preserving the exact
     fixture format that the Java suite uses (same `scenarioName` / state
     machine fields so fixtures remain usable by both runners).

```go
// Fixture file written during recording — identical format to Java version
{
  "scenarioName": "chat-sequence",
  "requiredScenarioState": "Started",        // "step-N" for N > 1
  "newScenarioState": "step-2",              // omitted on last fixture
  "request": { "method": "POST", "urlPath": "/v1/chat/completions" },
  "response": {
    "status": 200,
    "headers": { "Content-Type": "application/json" },
    "body": "<captured OpenAI response body>"
  }
}
```

The `scenarioName` / state-machine fields are written for Java compatibility
but are ignored by the Go playback reader, which simply walks `NNN.json` files
in lexical order.

The mock server is started once in `TestMain` and shared across all scenarios;
the per-scenario routing (fixture counter / journal) is stored in `SiteScenario`
and looked up via the `X-Scenario-ID` header.

### Memory Service Startup

```go
// site_test.go
func TestMain(m *testing.M) {
    if os.Getenv("RUN_SITE_TESTS") == "" && !buildTagActive() {
        os.Exit(0)
    }

    ctx := context.Background()
    dbURL := testpg.StartPostgres(/* global t */)

    cfg := config.DefaultConfig()
    cfg.Mode = config.ModeTesting   // bearer token = user ID
    cfg.DBURL = dbURL
    cfg.CacheType = "none"
    cfg.Listener.Port = 0
    // ... minimal config

    srv, _ := serve.StartServer(ctx, &cfg)
    memServiceURL = fmt.Sprintf("http://localhost:%d", srv.Running.Port)

    os.Exit(m.Run())
}
```

`ModeTesting` means `Authorization: Bearer alice-abc12345` → user ID `alice-abc12345`.
No OIDC or external auth needed.

### Astro Build Prerequisite

The Go runner reads `test-scenarios.json` from:
1. `SITE_TESTS_JSON` env var (if set), else
2. `<projectRoot>/site-tests/target/generated-test-resources/test-scenarios.json`

Project root detection: walk parent directories from `runtime.Caller(0)` until
`go.mod` is found.

If `test-scenarios.json` does not exist, `TestSiteDocs` calls `t.Skip("test-scenarios.json not found; run Astro build first")`.

To run the full pipeline:
```bash
cd site && npm ci && npm run build  # generates test-scenarios.json
go test -tags=site_tests ./internal/sitebdd/ -v -count=1
```

## Testing

The new Go suite validates itself by running the same doc scenarios that the Java
suite currently runs. No new scenario content is added; the feature files are
machine-generated from the same `test-scenarios.json` source.

Example generated scenario (gherkin):
```gherkin
@quarkus @checkpoint_quarkus_examples_checkpoint_01
Scenario: [quarkus] Getting Started Tutorial - checkpoint-01
  Given checkpoint "quarkus/examples/checkpoint-01" is active
  When I build the checkpoint
  Then the build should succeed

  When I start the checkpoint
  Then the application should be running

  When I execute curl command:
    """
    curl -s http://localhost:9090/api/conversations \
      -H "Authorization: Bearer alice"
    """
  Then the response status should be 200
  And the response should be json with items array

  When I stop the checkpoint
```

## Tasks

- [x] Create `internal/sitebdd/` package with build tag `//go:build site_tests`
- [x] Implement `scenarios.go`: JSON schema structs, `loadScenarios()`, `generateFeatureFiles()`
- [x] Implement `steps_checkpoint.go`: all `Given/When/Then` checkpoint lifecycle steps,
      port allocation, subprocess management, Python/Quarkus/Spring detection
- [x] Implement `steps_curl.go`: curl block parser (method/headers/body extraction),
      user & port substitution, retry logic, response normalization, assertion steps
- [x] Implement `mock_openai.go`: in-process HTTP mock server,
      fixture loading from `site-tests/openai-mock/fixtures/`, per-scenario counter;
      recording mode (proxy to real OpenAI, capture journal, `saveJournal()` writes
      WireMock-compatible `NNN.json` files)
- [x] Implement `site_test.go`: `TestSiteDocs` (Postgres + memory service + OpenAI mock
      startup, feature generation, godog runner with `Concurrency`)
- [x] Add `internal/sitebdd/FACTS.md` with gotchas as they are discovered
- [ ] Verify all existing Java site-test scenarios pass with the Go runner
- [ ] Add `go test -tags=site_tests` step to CI pipeline (optional, skipped by default
      in PR builds; only on merge to main or with explicit label)

## Files to Modify

| File | Change |
|------|--------|
| `internal/sitebdd/site_test.go` | **new** — `TestSiteDocs` runner |
| `internal/sitebdd/scenarios.go` | **new** — JSON loading + feature file generation |
| `internal/sitebdd/checkpoint.go` | **new** — `SiteScenario` struct, port allocation, process management |
| `internal/sitebdd/steps_curl.go` | **new** — curl parser + assertion step definitions |
| `internal/sitebdd/steps_checkpoint.go` | **new** — checkpoint lifecycle godog step registrations |
| `internal/sitebdd/mock_openai.go` | **new** — in-process OpenAI mock server + recording |
| `internal/sitebdd/FACTS.md` | **new** — module-specific gotchas |

No existing files require modification. The Java `site-tests/` Maven module is
not removed; both suites can coexist during the transition period.

## Verification

```bash
# 1. Generate test-scenarios.json (prerequisite)
cd site && npm ci && npm run build
cd ..

# 2. Build check (ensure no compile errors)
go build -tags=site_tests ./internal/sitebdd/

# 3. Run full site docs suite
go test -tags=site_tests ./internal/sitebdd/ -v -count=1 2>&1 | tee site-test.log

# 4. Search for failures
grep -E "FAIL|panic|Error" site-test.log

# 5. Run only quarkus scenarios
go test -tags=site_tests ./internal/sitebdd/ -v -count=1 \
  -godog.tags=@quarkus 2>&1 | tee site-test-quarkus.log

# 6. Run only a specific checkpoint
go test -tags=site_tests ./internal/sitebdd/ -v -count=1 \
  -godog.tags=@checkpoint_quarkus_examples_checkpoint_01

# 7. Record fixtures for checkpoints that have none (incremental)
SITE_TEST_RECORD=true OPENAI_API_KEY=sk-... \
  go test -tags=site_tests ./internal/sitebdd/ -v -count=1

# 8. Re-record all fixtures from scratch
SITE_TEST_RECORD=all OPENAI_API_KEY=sk-... OPENAI_MODEL=gpt-4o \
  go test -tags=site_tests ./internal/sitebdd/ -v -count=1 \
  -godog.tags=@checkpoint_quarkus_examples_checkpoint_01
```

## Design Decisions

**Why build tag instead of env var guard?**
A build tag (`//go:build site_tests`) ensures the package is not compiled at all
during regular `go test ./...` runs, preventing import graph expansion (e.g.
`testcontainers` pulling in Docker daemon checks at init time). The env var guard
alone would still compile the package. Both mechanisms together give defence in
depth: the tag excludes compilation; `RUN_SITE_TESTS` (checked in `TestMain`)
provides a runtime override for environments where recompiling with tags is
inconvenient.

**Why scenario-scoped UUID instead of port as the user suffix?**
The port was used in the Java version because it was already allocated per scenario
and unique. Using a UUID is more explicit and decouples user identity from
networking. It also makes responses clearer in failure output:
`alice-a1b2c3d4` is more recognizable than `alice-10092`.

**Why in-process OpenAI mock instead of WireMock container?**
WireMock requires Java (the exact thing being removed) or a Docker container.
An in-process `net/http/httptest.Server` has zero external dependencies, starts
in microseconds, and runs in the same process so no network routing is needed for
the mock. The existing fixture JSON files are reused verbatim; only the HTTP
routing logic is re-implemented in Go.

**Why `serve.StartServer()` instead of connecting to Docker Compose?**
Self-contained tests that start their own memory service can run in CI without
requiring `docker compose up` first. The existing `testpg.StartPostgres()`
helper creates an isolated Postgres container via `testcontainers-go`, so the
Go suite is fully hermetic. Developers who want faster iteration can still point
`MEMORY_SERVICE_URL` at a running instance and skip the in-process startup.

**Why one feature file per source MDX page?**
Grouping by source page keeps test output organized (each file maps to a tutorial)
and allows `godog.Tags` filtering to select individual tutorials without touching
the runner code.

**Why an in-memory journal instead of WireMock's admin API for recording?**
WireMock's journal is polled via an HTTP admin endpoint after the fact. The Go
approach intercepts responses in the handler itself — each captured call is
appended to `SiteScenario.Journal` atomically. This avoids the polling round-trip
and works correctly when multiple scenarios record concurrently because the journal
is per-scenario, not global. Fixture files written by the Go recorder are byte-for-
byte compatible with the Java WireMock format (same `scenarioName` / state-machine
fields), so fixtures created by either runner are interchangeable.

**Why keep `localhost:9090` in feature files?**
The canonical port in the docs is 9090. Keeping it in generated feature files
means the file content remains readable and matches what a developer sees in the
documentation. Port substitution at runtime (in `steps_curl.go`) is a single
`strings.ReplaceAll` per curl block — simple and centralized.
