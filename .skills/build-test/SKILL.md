---
name: build-test
description: Use when you need build or dev mode commands for memory-service.
---

# Build & Development

## Dev Mode (with live reload)
```bash
./mvnw quarkus:dev -pl memory-service          # backend on :8080
./mvnw quarkus:dev -pl quarkus/examples/chat-quarkus  # demo chat app on :8081
```

Dev Services auto-starts dependencies (Postgres, Keycloak, Redis) when Docker is available.

## Testing
```bash
./mvnw test                    # unit tests
./mvnw verify -DskipITs=false  # include integration tests
```

## Debugging Build Failures
```bash
./mvnw compile > compile.log 2>&1
rg -n "ERROR|FAIL|Exception|\\[ERROR\\]" compile.log

./mvnw test > test.log 2>&1
rg -n "ERROR|FAIL|Exception|\\[ERROR\\]" test.log
```
