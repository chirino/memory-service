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
task dev:memory-service
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

go test ./... > test.log 2>&1
rg -n "ERROR|FAIL|panic|--- FAIL:" test.log
```
