# Site BDD Module Facts

## Running the tests

```bash
# Prerequisite: generate test-scenarios.json from the Astro build
cd site && npm ci && npm run build && cd ..

# Compile check (fast, no Docker needed)
go build -tags=site_tests ./internal/sitebdd/

# Run all site-docs scenarios
go test -tags=site_tests ./internal/sitebdd/ -v -count=1

# Run only quarkus scenarios
go test -tags=site_tests ./internal/sitebdd/ -v -count=1 -godog.tags=@quarkus

# Run only one checkpoint
go test -tags=site_tests ./internal/sitebdd/ -v -count=1 \
  -godog.tags=@checkpoint_quarkus_examples_chat_quarkus_01_basic_agent

# Record fixtures for checkpoints that have none
SITE_TEST_RECORD=true OPENAI_API_KEY=sk-... \
  go test -tags=site_tests ./internal/sitebdd/ -v -count=1

# Re-record all fixtures
SITE_TEST_RECORD=all OPENAI_API_KEY=sk-... OPENAI_MODEL=gpt-4o \
  go test -tags=site_tests ./internal/sitebdd/ -v -count=1
```

## Architecture

The test suite reads `site/dist/test-scenarios.json`
(produced by `cd site && npm run build`) and generates Gherkin `.feature` files at
runtime into a temp directory. Those features are handed to `godog` with
`Concurrency: runtime.NumCPU()`.

Before running scenarios, `TestSiteDocs` installs Java checkpoint dependencies into
the local Maven repo (`:memory-service-extension-deployment` and
`:memory-service-spring-boot-starter`). This avoids parallel checkpoint builds
failing to resolve `999-SNAPSHOT` artifacts.

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

## OpenAI mock routing

Checkpoint subprocesses receive `OPENAI_API_KEY=sitebdd-<uid>` (not a real key).
The in-process mock reads the Authorization header to identify which scenario's
fixture counter / recording journal to use.  In recording mode the mock proxies to
real OpenAI using `OPENAI_API_KEY` from the test environment.

## Checkpoint framework detection

| Indicator | Framework |
|-----------|-----------|
| `pyproject.toml` present | Python (uvicorn) |
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
The framework is the first path segment of the checkpoint ID
(e.g., `quarkus/examples/chat-quarkus/01-basic-agent` → framework=`quarkus`,
name=`01-basic-agent`).

The Go playback reader ignores WireMock scenario-state fields (`scenarioName`,
`requiredScenarioState`, `newScenarioState`) and simply serves files in
lexical/numeric order. The recorder writes those fields for Java WireMock
compatibility.

## SITE_TEST_RECORD env var

| Value | Behaviour |
|-------|-----------|
| _(unset or "false")_ | Playback only |
| `true` | Record checkpoints with no existing fixtures; play back the rest |
| `all` / `force` | Re-record all checkpoints (overwrites existing fixtures) |

## Port allocation

Checkpoint apps start on ports 10090, 10091, … allocated via an `atomic.Int32`
counter. Each scenario gets its port in `Given checkpoint X is active`.  If a port
is somehow already in use when the checkpoint tries to start, the `waitForPort`
check will time out — investigate with `lsof -i :<port>`.

## Common failures

**`test-scenarios.json not found`**: Run `cd site && npm run build`.

**`go.mod not found walking up`**: The `findProjectRoot` function walks parent
directories from `internal/sitebdd/scenarios.go`. If the package was moved, update
the walk logic or set `SITE_TESTS_JSON` to an absolute path.

**Checkpoint doesn't respond within 90s**: Check the checkpoint app logs printed
with prefix `[checkpoint:<port>]`. Common causes: wrong memory service URL, missing
dependency, build not run first.

**Recording fails**: Ensure `OPENAI_API_KEY` is set in the environment. The mock
proxies to `https://api.openai.com` (or `OPENAI_API_BASE` if set).
