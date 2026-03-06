# Site BDD Module Facts

## Running the tests

```bash
# Prerequisite: generate test-scenarios.json from the Astro build
cd site && npm ci && npm run build && cd ..

# Compile check (fast, no Docker needed)
go build -tags=site_tests ./internal/sitebdd/

# Run all site-docs scenarios
go test -tags=site_tests ./internal/sitebdd/ -v -count=1

# Run targeted scenarios via task (preferred for reruns)
GODOG_TAGS='@python-langchain and @checkpoint_python_examples_langchain_doc_checkpoints_05_response_resumption' \
  task test:site -- -count=1

# Run only quarkus scenarios
go test -tags=site_tests ./internal/sitebdd/ -v -count=1 -godog.tags=@quarkus

# Run only one checkpoint
go test -tags=site_tests ./internal/sitebdd/ -v -count=1 \
  -godog.tags=@checkpoint_quarkus_examples_chat_quarkus_01_basic_agent

# Record fixtures only for checkpoints that have none
SITE_TEST_RECORD=missing OPENAI_API_KEY=sk-... \
  go test -tags=site_tests ./internal/sitebdd/ -v -count=1

# Re-record all fixtures
SITE_TEST_RECORD=all OPENAI_API_KEY=sk-... OPENAI_MODEL=gpt-4o \
  go test -tags=site_tests ./internal/sitebdd/ -v -count=1

# Capture real curl responses for docs exampleOutput sync
SITE_TEST_CAPTURE_CURL_OUTPUT=all \
  go test -tags=site_tests ./internal/sitebdd/ -v -count=1

# Sync captured outputs into <CurlTest exampleOutput={...}> blocks
go run ./internal/cmd/sync_curl_examples --apply
```

Site BDD build/checkpoint subprocess output is controlled by
`SITE_TEST_BUILD_OUTPUT`:
- `on-fail` (default): capture output and replay only on failures
- `stream`: stream output live during the run
- `quiet`: suppress captured output replay even on failures

`task test:site` now sets `SITE_TEST_BUILD_OUTPUT=on-fail` by default, so
`gotestsum`/`go test -json` no longer forces streamed `[mvn]` logs.

`TestSiteDocs` reuses an existing `site/dist/test-scenarios.json` if present and
only runs `site npm run build` when the file is missing. If docs changed, rebuild
the site before running site tests to avoid stale scenarios.

After recording fixtures, update scenario expectations if model outputs changed.
In practice this means adjusting the source MDX `<CurlTest steps={...}>`
assertions (and any expected JSON/text blocks) so generated feature files match
the newly recorded fixture content.

## Architecture

The test suite reads `site/dist/test-scenarios.json`
(produced by `cd site && npm run build`) and generates Gherkin `.feature` files at
runtime into a temp directory. Those features are handed to `godog` with
`Concurrency: runtime.NumCPU()` (falling back to 1 if the CPU count is unknown).

Before running scenarios, `TestSiteDocs` installs Java checkpoint dependencies into
the local Maven repo (`:memory-service-extension-deployment` and
`:memory-service-spring-boot-starter`). This avoids parallel checkpoint builds
failing to resolve `999-SNAPSHOT` artifacts.

Scenario execution is wave-gated. Up to `runtime.NumCPU()` scenarios are admitted
into a wave, and those scenarios can build and start checkpoints concurrently.
The first curl step in each scenario waits until every scenario in the current
wave has either reached `the application should be running` or exited early.
Only after the whole wave drains can the next wave begin building. This prevents
curl traffic from overlapping with checkpoint build/start work.

Strict JSON assertions (`response body should be json`) replay the last GET request
up to 4 times with short backoff before failing. This stabilizes checks for
eventually-consistent writes (for example, delayed AI history entry persistence
under high parallelism).

## User isolation

Every scenario gets an 8-hex-char UUID suffix (`ScenarioUID`). All HTTP calls
substitute canonical user names:
- `Authorization: Bearer alice` → `Authorization: Bearer alice-<uid>`
- `"userId": "alice"` → `"userId": "alice-<uid>"`

Response bodies are normalized back before assertions (`alice-<uid>` → `alice`).
This keeps feature file content matching the documentation.

Site BDD also enforces cross-scenario UUID uniqueness for executed curl requests.
A shared UUID (for example, conversation IDs reused in two scenario files) now
fails the later scenario with a registry conflict error to prevent data races.

## OpenAI mock routing

Checkpoint subprocesses receive `OPENAI_API_KEY=sitebdd-<uid>` (not a real key).
The in-process mock reads the Authorization header to identify which scenario's
fixture counter / recording journal to use.  In recording mode the mock proxies to
real OpenAI using `OPENAI_API_KEY` from the test environment.

Playback fallback is now fatal: if a chat completion request has no registered
scenario state or no matching fixture, the mock logs a FATAL line and exits the
test process via `os.Exit(2)` to force fixture/isolation fixes. Before exiting,
the mock force-kills all currently tracked checkpoint subprocesses to avoid
leaking background servers.

## Checkpoint framework detection

| Indicator | Framework |
|-----------|-----------|
| `pyproject.toml` present | Python (uvicorn) |
| `package.json` present (without `pyproject.toml`) | Node/TypeScript (`npm run start`) |
| `target/quarkus-app/quarkus-run.jar` present | Quarkus |
| Any `*.jar` in `target/` (not -sources.jar) | Spring Boot |

## Memory service env vars for checkpoint apps

| Env var | Purpose |
|---------|---------|
| `MEMORY_SERVICE_URL` | Generic; used by most apps |
| `QUARKUS_REST_CLIENT_MEMORY_SERVICE_URL` | Quarkus MicroProfile REST client |
| `SPRING_MEMORY_SERVICE_BASE_URL` | Spring framework override |

If a checkpoint app doesn't pick up the URL from one of these, add the appropriate
env var in `checkpoint.go → startCheckpoint()` and document it here.

## Fixture file format

Fixtures live in `internal/sitebdd/testdata/openai-mock/fixtures/<framework>/<checkpoint-name>/NNN.json`.
The framework is derived from the checkpoint ID
(e.g., `java/quarkus/examples/chat-quarkus/01-basic-agent` → framework=`quarkus`,
name=`01-basic-agent`).

Special framework mappings:
- `python/examples/langchain/...` → `python-langchain`
- `python/examples/langgraph/...` → `python-langgraph`
- `typescript/examples/vecelai/...` → `typescript-vecelai`

The Go playback reader ignores WireMock scenario-state fields (`scenarioName`,
`requiredScenarioState`, `newScenarioState`) and simply serves files in
lexical/numeric order. The recorder writes those fields for Java WireMock
compatibility.

## SITE_TEST_RECORD env var

| Value | Behaviour |
|-------|-----------|
| _(unset or "false")_ | Playback only |
| `missing` / `true` | Record checkpoints with no existing fixtures; play back the rest |
| `all` / `force` | Re-record all checkpoints (overwrites existing fixtures) |

## SITE_TEST_CAPTURE_CURL_OUTPUT env var

Captured curl outputs are stored in:
`internal/sitebdd/testdata/curl-examples/<framework>/<checkpoint>.json`

Each capture record includes `captureId`, request metadata, status, content type,
and body. `captureId` is generated from the docs route and curl-test ordinal:
`/docs/<route>#<n>`.

| Value | Behaviour |
|-------|-----------|
| _(unset)_ / `off` / `false` | Disabled |
| `missing` / `true` | Add captures only when `captureId` is absent |
| `all` / `force` | Upsert captures for all executed curl tests |

Any other value is treated as `off`.

## Port allocation

Checkpoint apps prefer ports 10090, 10091, … allocated via an `atomic.Int32`
counter, but now skip already-occupied ports and can fall back to an ephemeral OS
port if needed. This avoids false startup success when stale checkpoint processes
from earlier aborted runs are still listening.

A mutex-guarded checkpoint path registry also enforces that one checkpoint
directory can only be owned by one active scenario at a time; concurrent reuse
fails with a checkpoint isolation conflict.

Checkpoint startup now waits for `GET /ready` to return 2xx before any scenario
steps execute. This replaced the old fixed post-start sleep and makes startup
gating explicit at the application HTTP layer.

`task test:site` now hard-fails with an explicit message if gotest output contains
`(unknown)`, so abrupt test-process termination patterns are surfaced immediately.

`deriveFramework` in `internal/sitebdd/scenarios.go` recognizes framework paths
for `quarkus`, `spring`, `python-langchain`, `python-langgraph`, and
`typescript-vecelai`. This avoids UUID-registry collisions between TypeScript
and Python scenarios that intentionally reuse the same fixed conversation IDs in docs.


## Common failures

**`test-scenarios.json not found`**: Run `cd site && npm run build`.

**`go.mod not found walking up`**: The `findProjectRoot` function walks parent
directories from `internal/sitebdd/scenarios.go`. If the package was moved, update
the walk logic or set `SITE_TESTS_JSON` to an absolute path.

**Checkpoint doesn't respond within 90s**: Re-run with
`SITE_TEST_BUILD_OUTPUT=stream` to stream checkpoint app logs live. With the
default `on-fail` mode, checkpoint stdout/stderr is captured and replayed only
if the scenario fails. Common causes: wrong memory service URL, missing dependency,
build not run first.

**Recording fails**: Ensure `OPENAI_API_KEY` is set in the environment. The mock
proxies to `https://api.openai.com` (or `OPENAI_API_BASE` if set).

**`missing fixture during playback`**: Usually means the checkpoint executed more
OpenAI chat-completion calls than the fixture set contains for that scenario.
Inspect checkpoint logs for the first upstream failure and request sequence before
assuming fixture files are wrong.
