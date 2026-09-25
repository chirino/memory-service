---
name: build-test
description: Use when you need build, test, verification, or dev commands for memory-service across Go, Java, frontend, TypeScript, or Python modules, including which checks to run after a change.
---

# Build & Development

Run the smallest verification that covers the files you changed.

## What to run after a change

| Changed | Run |
|---|---|
| Go (`main.go`, `internal/`, `go.mod`) | `go build ./...` (or the affected packages), `go vet` on touched packages, and the relevant tests |
| Java, Quarkus, Spring | `./java/mvnw -f java/pom.xml -pl <module> -am compile`; a full `compile` only for broad changes |
| `frontends/chat-frontend/` | `npm run lint && npm run build` (or `task test:chat-frontend`) |
| `frontends/developer/` | `npm run lint && npm test && npm run build` (or `task test:developer-frontend`) |
| `typescript/` | `task generate:typescript` for formatting, plus the package's `npm run build` |
| `python/` | `python3 -m compileall` on changed files; `task verify:python` when packaging or gRPC stubs are affected |
| `internal/sitebdd/` | `go build -tags='site_tests sqlite_fts5' ./internal/sitebdd/` (see the `site-tests` skill) |
| Contracts | See the `api-contract-change` skill |

## Go

```bash
go build ./...
go test -tags='sqlite_fts5' ./internal/plugin/store/sqlite -count=1
task test:go
task dev:memory-service   # Air hot reload on :8082, dependencies via Docker Compose
```

### Go build tags and BDD auth modes

Use `task test:go` for the supported full Go test matrix. It runs production-auth tests and raw-bearer fixture tests separately:

- Normal Go tests and production-auth BDD runners use `sqlite_fts5`.
- Tests that authenticate users with raw bearer fixture values need both `sqlite_fts5` and `auth_testfixtures`.
- A bare `go test ./internal/bdd -run TestFeaturesSQLite` uses production auth and fails fixture-based scenarios with `raw bearer user assertions are not accepted in production builds`.

Targeted SQLite BDD runner with the feature-suite bearer fixtures:

```bash
CGO_ENABLED=1 go test -race -tags='sqlite_fts5 auth_testfixtures' \
  ./internal/bdd -run '^TestFeaturesSQLite$' -count=1
```

Production-auth BDD runners, matching `task test:go`:

```bash
CGO_ENABLED=1 go test -race -tags='sqlite_fts5' ./internal/bdd \
  -run 'TestFeaturesPg(APIKeys|Keycloak|KeycloakAuthClients)$' -count=1
```

## Java

The Maven wrapper and reactor root are under `java/`, so run Maven from the repo root as `./java/mvnw -f java/pom.xml ...`.

```bash
./java/mvnw -f java/pom.xml compile
./java/mvnw -f java/pom.xml test
./java/mvnw -f java/pom.xml -pl quarkus/memory-service-rest-quarkus -am compile
```

- Modules that depend on `memory-service-contracts` (for example the Quarkus REST client) need `-am` so the contracts module builds in the same reactor.
- After deleting or renaming protobuf messages, run `clean compile`. Stale classes under `target/generated-sources/grpc` survive a plain `compile`.
- Standalone apps (doc checkpoints, examples run outside the reactor) resolve snapshot modules from `~/.m2`. After changing a shared module they use, run `./java/mvnw -f java/pom.xml -pl <module> -am install -DskipTests`.
- Don't run `task test:java` and `task test:site` at the same time in one worktree. Both clean and build Java targets, and they race (`Failed to delete .../target`, missing generated classes).

## Frontend

```bash
cd frontends/chat-frontend && npm run lint && npm run build
cd frontends/developer && npm run lint && npm test && npm run build
```

## Python

```bash
python3 -m compileall python
task verify:python
```

## Reading failures

Test and build output is long. Redirect it to a file and search it instead of piping through `tail`:

```bash
go build ./... > build.log 2>&1
rg -n "ERROR|FAIL|panic|undefined:" build.log

task test:go > test.log 2>&1
rg -n "ERROR|FAIL|panic|--- FAIL:" test.log
```
