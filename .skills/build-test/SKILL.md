---
name: build-test
description: Use when you need build, test, or dev commands for memory-service across Go, Java, frontend, or Python modules.
---

# Build & Development

Use the smallest verification that matches the files you changed.

## Go
```bash
go build ./...
go test ./internal/plugin/store/sqlite -count=1
task test:go
task dev:memory-service
```

### Go build tags and BDD auth modes

Use `task test:go` for the supported full Go test matrix. It deliberately runs
production-auth tests and raw-bearer fixture tests separately:

- Normal Go tests and production-auth BDD runners use `sqlite_fts5`.
- Tests that authenticate users with raw bearer fixture values require both
  `sqlite_fts5` and `auth_testfixtures`.
- A bare `go test ./internal/bdd -run TestFeaturesSQLite` uses production auth
  and fails fixture-based scenarios with `raw bearer user assertions are not
  accepted in production builds`.

For a targeted SQLite BDD runner that uses the feature-suite bearer fixtures:

```bash
CGO_ENABLED=1 go test -race -tags='sqlite_fts5 auth_testfixtures' \
  ./internal/bdd -run '^TestFeaturesSQLite$' -count=1
```

For the production-auth BDD runners, match `task test:go`:

```bash
CGO_ENABLED=1 go test -race -tags='sqlite_fts5' ./internal/bdd \
  -run 'TestFeaturesPg(APIKeys|Keycloak|KeycloakAuthClients)$' -count=1
```

## Java
```bash
./java/mvnw -f java/pom.xml compile
./java/mvnw -f java/pom.xml test
```

## Frontend
```bash
cd frontends/chat-frontend
npm run lint
npm run build

cd frontends/developer
npm run lint
npm run build
```

## Python
```bash
python3 -m compileall python
task verify:python
```

## Debugging Build Failures
```bash
go build ./... > build.log 2>&1
rg -n "ERROR|FAIL|panic|undefined:" build.log

task test:go > test.log 2>&1
rg -n "ERROR|FAIL|panic|--- FAIL:" test.log
```
