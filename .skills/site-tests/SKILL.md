---
name: site-tests
description: "Use when editing documentation pages that contain <TestScenario>/<CurlTest> (site/src/pages/docs/**/*.mdx), doc checkpoint apps (*/examples/**/doc-checkpoints/**), internal/sitebdd/**, OpenAI mock fixtures or curl captures under internal/sitebdd/testdata/**, or site/src/components/{TestScenario,CurlTest,CodeFromFile,DocsSidebar}.astro, or when running or debugging `task test:site*`. Covers running, recording fixtures, syncing example output, and authoring or porting tutorials."
---

# Site Doc Tests

Tutorial pages are executable. Each `<TestScenario checkpoint="...">` builds and starts a checkpoint app, then runs the curl commands on the page and checks the assertions.

## Pipeline

1. `cd site && npm run build`: `site/src/components/TestScenario.astro` collects every ```` ```bash ```` block inside a `<TestScenario>` (not only the ones in `<CurlTest>`), plus the `steps` of the `<CurlTest>` that follows each one, and writes them to `site/dist/test-scenarios.json`. Blocks that share a `(checkpoint, page)` pair are merged.
2. `TestSiteDocs` (`internal/sitebdd/site_test.go`) loads that file. `scenarios.go` generates one feature per page in a temp dir, tagged `@<framework>`, `@wave_N`, `@checkpoint_<path>`. Never edit generated features; edit the MDX.
3. Before any scenario runs, it runs `clean install` for the Quarkus extension deployment and Spring Boot starter (Java pages) and `uv build` for `python/langchain` (Python pages).
4. godog runs the scenarios against one shared in-process Memory Service (Postgres via testcontainers, so Docker is required) plus an in-process OpenAI/OIDC mock (`mock*.go`). Pages whose route contains `/unix-domain-sockets` use a second, SQLite-backed server that listens on a Unix socket.
5. Waves (`wave_coordinator.go`): up to `siteScenarioConcurrency()` scenarios build and start together. No curl runs until every wave member is running or has exited. The next wave starts only after the current one drains. A checkpoint directory never appears twice in one wave.

## Running

```bash
task test:site > site-test.log 2>&1                   # everything; grep the log for failures
GODOG_TAGS='@quarkus' task test:site -- -count=1       # args after -- go to go test
GODOG_TAGS='@python-langchain and @checkpoint_python_examples_langchain_doc_checkpoints_05_response_resumption' task test:site -- -count=1
go build -tags='site_tests sqlite_fts5' ./internal/sitebdd/                      # compile check, no Docker
go test -tags='site_tests sqlite_fts5' ./internal/sitebdd/ -run 'TestDocs' -count=1  # MDX lint only, no Docker
```

- Tags: `@<framework>` is `concepts`, `quarkus`, `spring`, `python-langchain`, `python-langgraph`, or `typescript-vecelai` (from the page route, `deriveFramework`). `@checkpoint_<path>` is the checkpoint path lowercased with `/`, `-`, `.` replaced by `_`. `GODOG_TAGS` supports `and`, `or`, `not`, and parentheses.
- If you call `go test` yourself, pass `-timeout 20m` or more. The task already does (plus `-race`, `CGO_ENABLED=1`, and gotestsum).
- `TestSiteDocs` only builds the site when `site/dist/test-scenarios.json` is missing. After MDX edits, run `cd site && npm run build` or `rm -f site/dist/test-scenarios.json`, or the run uses stale scenarios.
- Devcontainer: `wt exec -- bash -c 'task test:site'`.
- Don't run `task test:java` and `task test:site` at the same time in one worktree. Both clean and build Java `target/` directories.
- If you build a Java checkpoint yourself after changing a shared Java snapshot module, first run `./java/mvnw -f java/pom.xml -pl <module> -am install -DskipTests`. Otherwise the checkpoint picks up stale jars from `~/.m2`. `TestSiteDocs` already installs the extension deployment and the Spring starter.

## Environment variables

| Variable | Values (from code) |
|---|---|
| `SITE_TEST_RECORD` | unset/`false`: playback. `missing`/`true`: record only checkpoints with no fixture files. `all`/`force`: re-record everything. |
| `SITE_TEST_CAPTURE_CURL_OUTPUT` | unset/`off`/`false`: off. `missing`/`true`: add captures whose ID is new. `all`/`force`: upsert every capture. Other values count as off. |
| `SITE_TEST_BUILD_OUTPUT` | `on-fail` (default; replay captured build/app output when a step fails), `stream` (live output), `quiet` (never replay). |
| `SITE_TEST_SCENARIO_CONCURRENCY` | Wave size and godog concurrency. Defaults to `runtime.NumCPU()`. |
| `SITE_TESTS_JSON` | Path to a `test-scenarios.json` to use in place of `site/dist/`. |
| `GODOG_TAGS` | godog tag filter (see above). |
| `GODOG_REPORT_DIR` / `GODOG_REPORT_FORMAT` | Write a `site-docs` report (`junit` default, or `cucumber`). |
| `OPENAI_API_KEY` | Recording only: the real key the mock proxies with. Required when recording. |
| `OPENAI_MODEL` | Recording only: model passed to checkpoints (default `gpt-4o`). In playback, checkpoints get `mock-gpt-markdown`. |
| `OPENAI_API_BASE` | Recording only: upstream base URL (default `https://api.openai.com`). |

## Recording OpenAI fixtures

```bash
OPENAI_API_KEY=sk-... task test:site:record-missing   # new checkpoints only
OPENAI_API_KEY=sk-... task test:site:record-all       # overwrite every fixture
```

- Layout: `internal/sitebdd/testdata/openai-mock/fixtures/<framework>/<checkpoint-leaf>/NNN.json` (`mock_openai.go` `fixtureDir`). The framework comes from the checkpoint path: `java/quarkus/` → `quarkus`, `java/spring/` → `spring`, `python/.../langchain/` → `python-langchain`, `python/.../langgraph/` → `python-langgraph`, `typescript/.../vecelai/` → `typescript-vecelai`.
- Each scenario gets `OPENAI_API_KEY=sitebdd-<uid>`. The mock uses that key to find the scenario and serves its fixture files in lexical order, one per chat-completions call. It ignores the WireMock fields (`scenarioName`, `requiredScenarioState`, `newScenarioState`); the recorder writes them for compatibility.
- Never copy fixtures between checkpoints. They carry scenario-specific IDs and text. Record them.
- Streaming code (`astream()`, SSE, response recording) needs `"Content-Type": "text/event-stream"` fixtures. The recorder always writes `application/json`, so fix the header on streamed recordings by hand. An optional `response.chunkedDribbleDelay` (`numberOfChunks`, `totalDuration` in ms) spreads the body over time; the existing streaming and resumption fixtures use it.
- After recording, update the MDX `<CurlTest steps>` assertions to match the new model text.

## Syncing example output

`<CurlTest exampleOutput={...}>` blocks are filled from real responses:

```bash
task test:site:update-curl-examples    # builds site, captures (default missing), then syncs
# or by hand:
SITE_TEST_CAPTURE_CURL_OUTPUT=all go test -tags='site_tests sqlite_fts5' -timeout 30m ./internal/sitebdd/ -run TestSiteDocs -count=1
go run ./internal/cmd/sync_curl_examples --apply    # omit --apply for a dry run
```

Captures go to `internal/sitebdd/testdata/curl-examples/<framework>/<checkpoint-leaf>.json`, keyed by `captureId` = `<route>#<n>` (for example `/docs/quarkus/getting-started/#4`). The sync tool counts `<CurlTest` tags in page order. The generator counts curl-running bash blocks in `<TestScenario>`s. Keep every tested curl block inside a `<CurlTest>` so the two counts line up. Captures keep the stable `response-resumption` route/checkpoint IDs.

## Writing CurlTests

Supported steps (`steps_curl.go`):

```
Then the response status should be 200
Then the response should contain "text"          # substring
Then the response should not contain "text"
Then the response should match pattern "regex"
Then the response should be json with items array   # top-level "data" must be an array
Then the response body should be text:           # """ block; whitespace-normalized substring
Then the response body should be json:           # """ block; see below
Then set "convId" to the json response field "data[0].id"
```

- JSON assertions are subset matches: extra object keys in the response are ignored. Arrays must have exactly the same length, so include every item visible to the user. List responses may include `"afterCursor": null` and other pagination fields, so leave them out of the expected JSON.
- Placeholders in expected JSON: `%{response.body.path}` (for example `%{response.body.data[0].id}`), `%{response.body}`, `%{context.name}` or `%{name}`. In curl commands, use `${name}` for values saved with `set`.
- Don't nest double quotes in `the response should contain "..."`. `"\"status\":\"ok\""` doesn't match the step regex. The step is then undefined, and because godog `Strict` is off, the rest of the scenario is skipped silently. Assert a plain token or use a JSON assertion.
- Retries: `response body should be json` replays the last request up to 6 times (750 ms apart), but only for GETs. `response should contain` does the same for GETs and `POST .../v1/conversations/resume-check`. Status, not-contains, and pattern checks never retry.
- Rewrites before execution: `localhost:9090` → checkpoint port. `localhost:8082` → shared Memory Service. On UDS pages, `/tmp/memory-service.sock` and `$HOME/.local/run/memory-service/api.sock` → shared socket (`--unix-socket` is supported). `alice`/`bob`/`charlie` in `Bearer <user>`, `"userId": "<user>"`, and `/<user>/` → `<user>-<uid>`, and responses are mapped back. `$(get-token)` / `$(get-token <user> <pass>)` → a mock-signed JWT (default user `bob`). `echo "..." > file` lines run for `-F @file` uploads. `function f() {...}` bodies are not executed. A trailing `| jq ...` is dropped.
- Conversation IDs (any UUID in a request) must be fresh (`uuidgen`) and must not be reused by any other scenario on any page. At runtime the UUID registry fails the second scenario to claim one. `TestDocsCurlUUIDsUniqueAcrossScenarios` checks this without Docker.

## Authoring and porting tutorials

- Tutorials are incremental. Copy the nearest completed checkpoint, which is not always `NN-1`, then make the changes a reader would make, then add `<CurlTest>`s that prove it works. Examples: Quarkus and Python forking, response recording, search, events, and Quarkus `03c-agent-subagent-workflows` start from `03-with-history`. Quarkus sharing starts from `05-response-resumption`, and Python sharing from `03-with-history`. The page's "Starting checkpoint" line is the source of truth.
- Checkpoint dirs: `java/{quarkus,spring}/examples/doc-checkpoints/`, `python/examples/{langchain,langgraph}/doc-checkpoints/`, `typescript/examples/vecelai/doc-checkpoints/`. `05-response-resumption` and `05b-response-resumption` both exist under TypeScript, so keep Taskfile entries explicit.
- Checkpoints must serve `GET /ready` (2xx) and read the env set in `checkpoint.go` `startCheckpoint`: `PORT`, `OPENAI_BASE_URL` (Spring gets `/v1` appended), `OPENAI_API_KEY`, `OPENAI_MODEL`, `MEMORY_SERVICE_URL`, `MEMORY_SERVICE_API_KEY=agent-api-key-1`, `MEMORY_SERVICE_CLIENT_URL`, and on UDS pages `MEMORY_SERVICE_UNIX_SOCKET`. Quarkus also gets `-Dquarkus.http.port`. Spring gets `--server.port` and `--memory-service.client.url`, plus mock OIDC issuer overrides when it uses `spring.security.oauth2`. If an app needs another variable, add it there.
- Python checkpoints need `pyproject.toml`, `app.py`, `uv.lock`, and a unique `pyproject` `name`.
- Embed code with `<CodeFromFile file="..." match="..." />` (optionally `before`/`after`) rather than `lines="N-M"`, which drifts when the file changes. `match` must be unique in the file. Only reference git-tracked files (for example `.env.example`, not `.env`). `TestDocsCodeFromFileReferences` checks that files are tracked and that line ranges are in bounds.
- Add new pages to `site/src/components/DocsSidebar.astro`.
- Standalone checkpoint poms don't inherit `java/pom.xml` dependency management. Keep Quarkus checkpoints' `quarkus.platform.version` and `langchain4j.version` (the quarkus-langchain4j version) equal to `quarkus.platform.version` / `quarkus-langchain4j.version` in `java/pom.xml`. Keep Spring checkpoints' `protobuf.version` override equal to the `protobuf.version` in `java/spring/pom.xml` used by `memory-service-proto-spring`.
- When an API changes, update the affected checkpoints, tutorial text, curl assertions, and fixtures together.

## Docs conventions

- Routes stay `/response-resumption/`, but titles, sidebar labels, and links say "Response Recording and Resumption".
- Framework event guides must say that browser-facing proxies forward history-channel entry notifications only, while trusted processors may subscribe to context entries directly against Memory Service.
- Don't restate contract details (enums, field semantics) in tutorials. Link to the concept pages or API reference.

## Debugging

- Start with `SITE_TEST_BUILD_OUTPUT=stream` to see builds and app logs live.
- "checkpoint did not start on port" / "failed readiness probe": the app didn't listen, or `/ready` wasn't 2xx within 90 s. Usual causes are a build failure, a missing `/ready`, or the app ignoring the injected env.
- The process exits with code 2 and logs `[openai-mock] FATAL: missing fixture during playback` or `no registered scenario state`. Either the app made more LLM calls than there are fixtures, or it isn't using `OPENAI_API_KEY`/`OPENAI_BASE_URL`. Check the request sequence in the app log before changing fixtures.
- `checkpoint isolation conflict`: two active scenarios claimed the same checkpoint dir. `uuid registry conflict`: two scenarios sent the same UUID (run `-run TestDocs`).
- Checkpoints use ports from 10090 upward and skip busy ones. Stale processes from aborted runs can hold those ports; kill them.
- `test-scenarios.json not found`: build the site. If the Rollup build fails with a missing `@rollup/rollup-<platform>-<arch>`, run `npm install` in `site/`.
- Devcontainer builds need `libsqlite3-dev` (sqlite-vec bindings need `sqlite3.h`).
