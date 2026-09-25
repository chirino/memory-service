---
name: api-contract-change
description: Use when adding or changing an API operation, request/response field, or query parameter, or when editing anything under contracts/ (OpenAPI in contracts/openapi/, protobuf in contracts/protobuf/). Covers regenerating clients, keeping the agent/admin REST and gRPC surfaces aligned, and the hand-written clients that must be updated by hand.
---

# API Contract Changes

`contracts/` is the source of truth:

- Agent REST API: `contracts/openapi/openapi.yml`
- Admin REST API: `contracts/openapi/openapi-admin.yml`
- gRPC (agent and admin services): `contracts/protobuf/memory/v1/memory_service.proto`

Describe behavior in the contract (field and operation descriptions), not in agent notes or prose docs.

## 1. Keep the four surfaces aligned

When you change an agent REST operation, check the matching agent gRPC, admin REST, and admin gRPC operations, and change each one where the operation makes sense there. If a surface intentionally differs, say why in the contract description or the enhancement doc.

For each changed operation, keep these in step:

- The OpenAPI and protobuf contracts
- Server handlers: `internal/plugin/route/**` (REST, wired through `internal/cmd/serve/wrapper_routes.go`) and `internal/grpc/server.go`
- Datastore queries in all three stores (`internal/plugin/store/{postgres,sqlite,mongo}`)
- BDD coverage for REST and gRPC (see the `go-bdd` skill)
- Site docs that show the operation

For conversation list, detail, or update changes, check both the public and the admin summary/detail payloads, filters, and mutation requests. Admin payloads may expose fields (such as `clientId`) that user-facing payloads must not.

## 2. Regenerate

```bash
task generate   # everything: Go (oapi-codegen + protoc), frontends, Python stubs, formatting
go generate .   # Go only: internal/generated/{api,apiclient,admin,pb}
```

Don't edit generated code. The Java REST and gRPC clients are generated during the Maven build:

```bash
./java/mvnw -f java/pom.xml -pl quarkus/memory-service-rest-quarkus,spring/memory-service-rest-spring -am compile
```

After deleting or renaming protobuf messages, use `clean compile` so stale generated classes don't survive.

OpenAPI generator traps are commented next to the schemas they affect in `openapi.yml` (for example `searchType`). Read those comments before restructuring a `oneOf`.

## 3. Update the hand-written clients

These map contract parameters by hand. A compile can pass while a new query parameter is silently dropped:

- Quarkus `UnixSocketRestClientFactory` (UDS REST shim) and `MemoryServiceProxy` (`java/quarkus/memory-service-extension/runtime`)
- Spring `MemoryServiceProxy` and its option records (`java/spring/memory-service-rest-spring`)
- Python `MemoryServiceProxy` (`python/langchain/memory_service_langchain/proxy.py`)
- TypeScript `createMemoryServiceProxy` (`typescript/vercelai/src/index.ts`)
- Demo app proxy routes that forward query parameters (`java/*/examples/chat-*`, `python/examples/*/chat-*`)

## 4. Verify

```bash
go build ./...
cd frontends/chat-frontend && npm run lint && npm run build
cd frontends/developer && npm run lint && npm test && npm run build
./java/mvnw -f java/pom.xml compile
```

Then run the BDD runners that cover the changed operations (see the `build-test` skill for tags).
