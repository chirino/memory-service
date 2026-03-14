---
status: implemented
---

# Enhancement 079: Client Unix Socket Support

> **Status**: Implemented.

## Summary

Complete the client side of [Enhancement 077](077-unix-socket-listener-support.md) so agent applications can connect to Memory Service over a Unix domain socket with the same level of effort as today’s TCP configuration. The language/framework packages in this repo should detect a UDS configuration setting and set up the correct REST and gRPC transports internally, without requiring agent apps to write custom transport code.

## Implementation Notes

This enhancement shipped with one shared UDS knob per stack:

1. Python / TypeScript: `MEMORY_SERVICE_UNIX_SOCKET`
2. Spring / Quarkus: `memory-service.client.url=unix:///absolute/path.sock`

Shipped behavior:

1. Python LangChain/LangGraph REST now uses `httpx` UDS transports, and gRPC derives `unix:///...` targets when the shared UDS setting is present.
2. TypeScript/Vercel AI REST now uses an `undici` agent with `connect.socketPath`, and gRPC derives `unix:///...` targets from the shared UDS setting.
3. Spring gRPC uses a Netty/JDK Unix-domain-socket channel, and Spring REST uses Reactor Netty with a custom UDS `ClientHttpConnector` that keeps `http://localhost` as the logical base URL. Because Reactor Netty emitted HTTP/3 over the UDS path during implementation, the shipped Spring REST connector also forces outbound UDS REST requests back to HTTP/1.1. This implementation detail is recorded in `WORKAROUNDS.md`.
4. Quarkus REST uses the extension's custom UDS HTTP client/proxy path, and Quarkus gRPC uses `grpc-netty-shaded` with `NioDomainSocketChannel` + `UnixDomainSocketAddress`.
5. Redirect handling for Quarkus response-recording remained out of scope.
6. Site docs now include a shared concept page and framework-specific UDS pages for Python LangChain, Python LangGraph, TypeScript/Vercel AI, Spring, and Quarkus.
7. `internal/sitebdd` now starts a shared UDS-backed local Memory Service for `/unix-domain-sockets/` docs pages, supports `curl --unix-socket ...`, and honors fixture `chunkedDribbleDelay` so response-resumption docs can test disconnect/resume flows deterministically.

## Motivation

The Go service already supports Unix domain socket listeners, but the client packages do not yet make that mode easy to consume.

Today:

1. Python REST helpers use plain `httpx.Client` / `AsyncClient` base URLs and do not expose UDS transport configuration.
2. Python gRPC helpers can take an explicit target string, but the default path still derives `host:port` from `MEMORY_SERVICE_URL`.
3. TypeScript REST helpers use plain `fetch(...)` with an HTTP URL and do not expose a Node UDS dispatcher/agent.
4. TypeScript gRPC helpers can take an explicit target string, but the default path still derives `host:port` from `MEMORY_SERVICE_URL`.
5. Spring REST wraps the generated OpenAPI client with `WebClient` and now accepts `memory-service.client.url` for both TCP and UDS.
6. Spring gRPC wraps generated stubs with `ManagedChannelBuilder.forTarget(...)` but does not expose a UDS-specific builder path.
7. Quarkus REST builds generated clients from a plain `baseUri`, and now accepts `memory-service.client.url` with either `http(s)` or `unix` schemes.
8. Quarkus gRPC response-recording code assumes `host + ":" + port` and redirect parsing also assumes TCP addresses.

That leaves agents in an awkward state: the server can listen on a Unix socket, but client apps must either keep using TCP or write stack-specific transport code outside the provided packages.

The goal of this enhancement is to make local-agent UDS deployments feel first-class:

1. The operator changes configuration, not application code.
2. REST and gRPC both follow that configuration automatically.
3. The client packages own all runtime-specific socket setup.
4. Generated clients remain generated; transport customization happens in the wrappers/builders around them.

## Design

### Scope

This enhancement covers:

1. Python LangChain/LangGraph package UDS support for REST and gRPC.
2. TypeScript Vercel AI package UDS support for REST and gRPC.
3. Spring client packages and Spring Boot auto-configuration UDS support for REST and gRPC.
4. Quarkus client/extension packages UDS support for REST and gRPC.
5. Site docs pages showing how to run a fully local Memory Service over UDS and how each framework connects to it.
6. Site BDD support and scenario coverage for those docs pages, including `curl --unix-socket ...` examples.
7. Test coverage proving client packages can talk to a UDS-backed Memory Service without application code changes.

This enhancement does not cover:

1. Windows named pipes.
2. Browser-side direct UDS access.
3. Changing the public OpenAPI or protobuf contracts.
4. Replacing generated REST/gRPC code with hand-maintained forks.
5. Cross-host transports; UDS remains a same-machine deployment mode.

### Documentation Deliverables

This enhancement should ship docs as a first-class deliverable.

Required pages:

1. A shared concept page under `site/src/pages/docs/concepts/` explaining Unix domain socket connectivity for local agents.
2. A framework-specific guide page under each supported framework docs tree:
   - `site/src/pages/docs/python-langchain/`
   - `site/src/pages/docs/python-langgraph/`
   - `site/src/pages/docs/typescript-vecelai/`
   - `site/src/pages/docs/spring/`
   - `site/src/pages/docs/quarkus/`
3. Sidebar entries for the new concept page and framework guide pages.

The docs should explicitly show a fully local single-machine stack:

1. `db-kind=sqlite`
2. `vector-kind=sqlite`
3. `cache-kind=local`
4. Unix socket listener enabled
5. Agent app configured only via the framework/client package config surface

The intention is that a user can copy one local-server command and one framework-specific client config snippet and have a working local setup without Dockerized Postgres, Redis, or Qdrant.

### User-Facing Configuration Model

Add a single user-facing Unix socket setting for each client package and make it apply to both REST and gRPC by default.

Canonical behavior:

1. If the client UDS setting is absent, current TCP behavior remains unchanged.
2. If the client UDS setting is present, REST and gRPC both use that socket unless a more specific explicit override is set.
3. Existing explicit gRPC target overrides still win over auto-derived values.
4. Agent code should not need to construct custom HTTP transports, dispatchers, Netty builders, or gRPC dialers.

Configuration precedence:

1. Explicit gRPC override
2. Explicit gRPC Unix socket override
3. Shared client Unix socket setting
4. Existing TCP URL / host:port derivation

Implementation preference:

1. Use one shared UDS knob per stack if the runtime/framework allows it cleanly.
2. Add more specific REST/gRPC UDS knobs only when a stack cannot practically support the one-knob model without unreasonable complexity.
3. Documentation should present the one-knob model first; any escape hatches stay secondary.

Planned config keys:

| Stack | Shared UDS config | Existing TCP config retained | Notes |
|------|-------------------|------------------------------|-------|
| Python | `MEMORY_SERVICE_UNIX_SOCKET` | `MEMORY_SERVICE_URL`, `MEMORY_SERVICE_GRPC_TARGET`, `MEMORY_SERVICE_GRPC_PORT` | Shared env var for REST + gRPC |
| TypeScript | `MEMORY_SERVICE_UNIX_SOCKET` | `MEMORY_SERVICE_URL`, `MEMORY_SERVICE_GRPC_TARGET` | Shared env var for REST + gRPC |
| Spring | `memory-service.client.url=unix:///...` | `memory-service.client.url=http://...`, `memory-service.grpc.target` | Shared property for REST + gRPC auto-config |
| Quarkus | `memory-service.client.url=unix:///...` | `memory-service.client.url=http://...`, existing Quarkus gRPC host/port config | Shared property for REST + gRPC auto-config |

Optional stack-specific gRPC UDS overrides may be added where needed internally, but they should not be the primary documented setup path.

Representative usage:

```bash
# Python / TypeScript agent app
export MEMORY_SERVICE_UNIX_SOCKET="$HOME/.local/run/memory-service/api.sock"
```

```properties
# Spring Boot app
memory-service.client.url=unix:///home/agent/.local/run/memory-service/api.sock
```

```properties
# Quarkus app
memory-service.client.url=unix:///home/agent/.local/run/memory-service/api.sock
```

### Local Single-Machine Quickstart Pattern

The shared docs story should use the same server startup shape across concept and framework pages so users see one coherent local deployment pattern.

Representative server startup:

```bash
memory-service serve \
  --db-kind=sqlite \
  --db-url=file:$HOME/.local/share/memory-service/memory.db \
  --vector-kind=sqlite \
  --cache-kind=local \
  --unix-socket=$HOME/.local/run/memory-service/api.sock
```

Equivalent env form:

```bash
export MEMORY_SERVICE_DB_KIND=sqlite
export MEMORY_SERVICE_DB_URL=file:$HOME/.local/share/memory-service/memory.db
export MEMORY_SERVICE_VECTOR_KIND=sqlite
export MEMORY_SERVICE_CACHE_KIND=local
export MEMORY_SERVICE_UNIX_SOCKET=$HOME/.local/run/memory-service/api.sock
memory-service serve
```

Docs requirements:

1. The concept page should explain why SQLite + SQLite vector + local cache + UDS is the recommended local-agent stack.
2. Each framework page should reuse that same server configuration before showing the framework-specific client setting.
3. The examples should stay aligned with the implemented local backends from [076](076-sqlite-datastore.md), [077](077-unix-socket-listener-support.md), and [078](078-local-cache-backend.md).
4. Sitebdd can validate the concept page by starting a checkpoint app that embeds the Memory Service server and configures it with the equivalent local-stack settings, rather than shelling out to a standalone `memory-service serve` subprocess.

### Transport-Neutral Endpoint Resolution

Each client package should resolve config into a small internal endpoint model rather than branching ad hoc in every call site.

Representative model:

```text
ResolvedMemoryServiceEndpoint
  restMode: tcp | unix
  grpcMode: tcp | unix
  restBaseUrl: string          # e.g. http://localhost
  grpcTarget: string           # e.g. localhost:8082 or unix:///.../api.sock
  unixSocketPath: string | nil
```

Rules:

1. In TCP mode, preserve current URL/host:port behavior.
2. In Unix mode, use a logical HTTP origin of `http://localhost` for REST request construction.
3. In Unix mode, derive gRPC target as `unix:///absolute/path.sock`.
4. All wrappers/builders should consume the resolved endpoint instead of reparsing env vars independently.

This reduces drift between REST and gRPC paths and keeps future client packages aligned.

### Python

Python should expose UDS support entirely inside `memory-service-langchain`.

REST:

1. Add a `unix_socket` option to the shared request helpers and high-level wrappers.
2. When `unix_socket` is present, construct `httpx` transports with UDS enabled rather than relying on `base_url` alone.
3. Use `http://localhost` as the logical base URL for request paths.

gRPC:

1. If `MEMORY_SERVICE_UNIX_SOCKET` is set and no explicit `grpc_target` override is present, derive the gRPC target as `unix:///...`.
2. Apply this to response recording, replay, check, and cancel helpers.

Implementation detail:

1. Keep generated gRPC stubs unchanged.
2. Keep UDS transport setup in `request_context.py`, `history_middleware.py`, `checkpoint_saver.py`, `response_recording_manager.py`, and `response_recorder.py`.

### TypeScript

TypeScript should expose UDS support entirely inside `@chirino/memory-service-vercelai`.

REST:

1. Add a `unixSocket?: string` option to the package’s internal proxy/request helpers.
2. In Node runtimes, use a custom dispatcher/agent that connects to the configured socket path while preserving `http://localhost` request URLs.
3. Keep the current plain-`fetch` TCP path as the default.

gRPC:

1. If `MEMORY_SERVICE_UNIX_SOCKET` is set and no explicit `MEMORY_SERVICE_GRPC_TARGET` is present, derive the gRPC target as `unix:///...`.
2. Apply this to recorder, replay, resume-check, and cancel flows.

Constraints:

1. This package is a Node server-side package, not a browser package.
2. UDS support only needs to work in Node environments where the package already runs.

### Spring

Spring should support UDS without requiring application code to replace beans manually.

REST:

1. Extend `MemoryServiceClientProperties` with `unixSocket`.
2. Update `MemoryServiceClients.createWebClient(...)` to build a Reactor Netty HTTP client that uses the configured Unix socket when present.
3. Continue wrapping the generated OpenAPI `ApiClient`; do not patch generated sources.

gRPC:

1. Extend auto-configuration so `memory-service.client.url=unix:///...` drives gRPC setup when explicit `memory-service.grpc.target` is absent.
2. Use the Netty-backed grpc-java channel path required for Unix domain sockets.
3. Keep metadata/header interceptors and keepalive settings working in both TCP and UDS modes.

Auto-configuration behavior:

1. If `memory-service.grpc.target` is explicitly set, use it as today.
2. Else if `memory-service.client.url` uses the `unix` scheme, create a UDS-capable channel and skip URL-based `host:port` derivation.
3. Else derive `host:port` from `memory-service.client.url` as today.

### Quarkus

Quarkus should support UDS through the extension/runtime code, not through application-level transport code.

REST:

1. Extend `memory-service.client.url` so it accepts both `http(s)://...` and `unix:///...`.
2. Update `MemoryServiceApiBuilder` to choose a UDS-capable client path when that property is set.
3. Preserve the current URL-based path for TCP.

gRPC:

1. Stop assuming the initial response-recorder client must come from `quarkus.grpc.clients.responserecorder.host` + `port`.
2. When `memory-service.client.url` uses the `unix` scheme, build the recorder client/channel from a Unix target instead.
3. Skip `GrpcFromUrlConfigSource` host/port derivation when Unix socket mode is explicitly configured.

Compatibility goal:

1. Existing Quarkus apps should keep working unchanged over TCP.
2. A Quarkus app should switch to UDS by setting one property.

### Generated Client Policy

The generated REST and gRPC code should remain transport-agnostic.

Rules:

1. Do not hand-edit generated OpenAPI or gRPC output.
2. Inject UDS behavior through wrapper/builders, connection factories, and auto-configuration.
3. If a generated-client integration path cannot support UDS through existing hooks, replace only the wrapper layer around it, not the generated code itself.

### Documentation

Update client docs and examples to show that local agents can connect to a UDS-backed Memory Service with configuration only.

Required docs content:

1. A new concept page, for example `site/src/pages/docs/concepts/unix-domain-sockets.mdx`, that explains local UDS deployments and shows the fully local server startup using SQLite datastore, SQLite vector store, local cache, and UDS listener config.
2. A framework-specific guide page for Python LangChain showing how an agent app connects via `MEMORY_SERVICE_UNIX_SOCKET`.
3. A framework-specific guide page for Python LangGraph showing how an agent app connects via `MEMORY_SERVICE_UNIX_SOCKET`.
4. A framework-specific guide page for TypeScript/Vercel AI showing how an agent app connects via `MEMORY_SERVICE_UNIX_SOCKET`.
5. A framework-specific guide page for Spring showing how an agent app connects via `memory-service.client.url=unix:///...`.
6. A framework-specific guide page for Quarkus showing how an agent app connects via `memory-service.client.url=unix:///...`.
7. Each page should include tested code/config snippets and `<CurlTest>` coverage through sitebdd.
8. The concept page should include tested `curl --unix-socket ...` examples against the local Memory Service.
9. A note that browser/SPA code still cannot connect directly to UDS.
10. Java docs should show property keys only; environment-variable forms are intentionally derived rather than documented inline on these pages.

Suggested doc routes:

1. `/docs/concepts/unix-domain-sockets/`
2. `/docs/python-langchain/unix-domain-sockets/`
3. `/docs/python-langgraph/unix-domain-sockets/`
4. `/docs/typescript-vecelai/unix-domain-sockets/`
5. `/docs/spring/unix-domain-sockets/`
6. `/docs/quarkus/unix-domain-sockets/`

Each framework page should:

1. Start from the same local Memory Service startup command.
2. Continue from the existing response-resumption tutorial checkpoint for that framework, with the page-specific code change focused on configuring UDS instead of TCP.
3. Reuse an existing tutorial checkpoint or add a dedicated UDS checkpoint so sitebdd can verify the page end-to-end.
4. Demonstrate request resumption working over UDS via the page’s verification `curl` commands.
5. Include at least one proxied REST API call over UDS to show non-gRPC client traffic is also working.

### Non-Goals

This enhancement is not intended to:

1. Introduce a new public API endpoint.
2. Make frontend browser code connect to a Unix socket.
3. Require users to write custom transport setup in their applications.
4. Add backward-compatibility shims for deprecated config names.

## Testing

### Implemented Coverage

The shipped verification focuses on package builds, framework checkpoint builds, and sitebdd integration coverage rather than stack-specific unit tests.

Implemented coverage:

1. Python package compile validation for the updated LangChain/LangGraph modules.
2. TypeScript package build validation for `typescript/vercelai`.
3. Targeted Maven compile validation for the Spring and Quarkus runtime modules that own the new transport behavior.
4. Sitebdd concept/framework coverage for the new UDS docs pages.
5. Docs scenarios proving:
   - `curl --unix-socket ...` works against a local UDS-backed Memory Service
   - each framework can switch to UDS with configuration only
   - response resumption works over UDS
   - one proxied REST API call works over UDS

### Site BDD Coverage

Docs pages added by this enhancement must be executable through sitebdd.

Required sitebdd work:

1. Add support in `internal/sitebdd` for `curl --unix-socket ...` examples.
2. Add scenario/build support for docs that need a checkpoint app to embed and start Memory Service with SQLite datastore, SQLite vector backend, local cache, and UDS listener config.
3. Add framework-specific tested pages using `<TestScenario>` and `<CurlTest>`.
4. Ensure the concept page is also exercised via sitebdd, not just framework pages.

Docs-test requirements:

1. The concept page must prove that a local Memory Service can be started with SQLite + local cache + SQLite vector + UDS and accepts `curl --unix-socket` requests.
2. Each framework page must prove that the documented framework-specific config is enough for the agent app to connect via UDS.
3. Each docs scenario must use unique conversation UUIDs and fixture isolation consistent with current sitebdd rules.
4. Each framework page must prove both response resumption and one proxied REST API call over UDS.

Representative gherkin:

```gherkin
Scenario: Python agent package uses Unix socket transport automatically
  Given the memory service is listening on unix socket "$HOME/.local/run/memory-service/api.sock"
  And a Python agent app sets "MEMORY_SERVICE_UNIX_SOCKET" to "$HOME/.local/run/memory-service/api.sock"
  When the app appends a conversation entry
  Then the request succeeds without any transport code in the app
  And response recording over gRPC also succeeds
```

```gherkin
Scenario: Spring Boot switches from TCP to Unix socket by configuration only
  Given the memory service is listening on unix socket "$HOME/.local/run/memory-service/api.sock"
  And a Spring Boot app sets "memory-service.client.url" to "unix://${HOME}/.local/run/memory-service/api.sock"
  When the app calls the generated REST client and the response recorder
  Then both calls succeed without custom bean overrides
```

```gherkin
Scenario: Concept page local stack starts Memory Service on a Unix socket
  Given a checkpoint app embeds the memory service configured with sqlite datastore, sqlite vector store, local cache, and unix socket "$HOME/.local/run/memory-service/api.sock"
  When a docs example runs curl with "--unix-socket $HOME/.local/run/memory-service/api.sock" against "/ready"
  Then the response status should be 200
  And a docs example can create and list a conversation through the same socket
```

## Tasks

- [x] Add a shared UDS config setting to the Python package and route both REST and gRPC through it.
- [x] Add a shared UDS config setting to the TypeScript package and route both REST and gRPC through it.
- [x] Extend Spring client properties and auto-configuration to support UDS for REST and gRPC.
- [x] Extend Quarkus client/extension configuration to support UDS for REST and gRPC.
- [x] Refactor endpoint resolution so wrappers/builders consume a transport-neutral resolved endpoint instead of reparsing env vars independently.
- [x] Preserve generated client code as transport-agnostic and inject UDS setup only in wrapper/builders.
- [x] Add integration coverage against a UDS-listening Memory Service.
- [x] Add a shared concept page for local UDS deployments using SQLite datastore, SQLite vector store, local cache, and Unix socket listener configuration.
- [x] Add framework-specific UDS guide pages for Python LangChain, Python LangGraph, TypeScript/Vercel AI, Spring, and Quarkus.
- [x] Add or adapt response-resumption checkpoint apps so each new docs page can be exercised by sitebdd with a UDS-focused code/config diff.
- [x] Extend `internal/sitebdd` to support `curl --unix-socket ...` and any Memory Service startup plumbing needed by the new docs scenarios.
- [x] Update examples and docs to show configuration-only UDS usage for local agents.

## Files to Modify

| File | Purpose |
|------|---------|
| `docs/enhancements/079-client-unix-socket-support.md` | Enhancement proposal |
| `python/langchain/memory_service_langchain/request_context.py` | Shared REST request helper UDS transport support |
| `python/langchain/memory_service_langchain/history_middleware.py` | Sync REST client UDS support |
| `python/langchain/memory_service_langchain/checkpoint_saver.py` | Direct REST checkpoint calls over UDS |
| `python/langchain/memory_service_langchain/response_recording_manager.py` | gRPC target resolution from shared UDS config |
| `python/langchain/memory_service_langchain/response_recorder.py` | gRPC channel creation over UDS |
| `typescript/vercelai/src/index.ts` | REST fetch dispatcher/agent support and gRPC target derivation from UDS config |
| `java/spring/memory-service-rest-spring/src/main/java/io/github/chirino/memoryservice/client/MemoryServiceClientProperties.java` | Add shared UDS property |
| `java/spring/memory-service-rest-spring/src/main/java/io/github/chirino/memoryservice/client/MemoryServiceClients.java` | Build UDS-capable WebClient transport |
| `java/spring/memory-service-proto-spring/src/main/java/io/github/chirino/memoryservice/grpc/MemoryServiceGrpcProperties.java` | Optional explicit gRPC UDS config surface if needed |
| `java/spring/memory-service-proto-spring/src/main/java/io/github/chirino/memoryservice/grpc/MemoryServiceGrpcClients.java` | Build UDS-capable grpc-java channels |
| `java/spring/memory-service-spring-boot-autoconfigure/src/main/java/io/github/chirino/memoryservice/spring/boot/MemoryServiceAutoConfiguration.java` | Auto-config precedence for TCP vs UDS |
| `java/spring/memory-service-spring-boot-autoconfigure/src/main/java/io/github/chirino/memoryservice/spring/autoconfigure/MemoryServiceAutoConfiguration.java` | Auto-config precedence for TCP vs UDS |
| `java/quarkus/memory-service-extension/runtime/src/main/java/io/github/chirino/memory/runtime/MemoryServiceApiBuilder.java` | REST client builder UDS support |
| `java/quarkus/memory-service-extension/runtime/src/main/java/io/github/chirino/memory/history/runtime/GrpcResponseRecordingManager.java` | Initial gRPC channel setup for UDS |
| `java/quarkus/memory-service-extension/runtime/src/main/java/io/github/chirino/memory/runtime/GrpcFromUrlConfigSource.java` | Skip TCP URL derivation when Unix socket mode is configured |
| `site/src/pages/docs/concepts/unix-domain-sockets.mdx` | Shared concept page for local UDS deployments |
| `site/src/pages/docs/python-langchain/unix-domain-sockets.mdx` | Python LangChain UDS guide |
| `site/src/pages/docs/python-langgraph/unix-domain-sockets.mdx` | Python LangGraph UDS guide |
| `site/src/pages/docs/typescript-vecelai/unix-domain-sockets.mdx` | TypeScript/Vercel AI UDS guide |
| `site/src/pages/docs/spring/unix-domain-sockets.mdx` | Spring UDS guide |
| `site/src/pages/docs/quarkus/unix-domain-sockets.mdx` | Quarkus UDS guide |
| `site/src/components/DocsSidebar.astro` | Sidebar entries for the new concept and framework pages |
| `site/src/pages/docs/configuration.mdx` | Cross-stack client configuration docs |
| `site/src/pages/docs/faq.mdx` | Clarify UDS client limitations and intended local-agent usage |
| `internal/sitebdd/steps_curl.go` | Add `curl --unix-socket` parsing/execution support |
| `internal/sitebdd/**` | Scenario/build support for UDS docs pages and local Memory Service startup |
| `internal/sitebdd/testdata/openai-mock/fixtures/**` | Fixtures for any new UDS tutorial checkpoints |

## Design Decisions

1. **One documented UDS knob per package**: The main user-facing setup should be a single shared Unix socket setting that drives both REST and gRPC. Separate knobs may exist internally, but they should not be the default guidance.
2. **Generated code stays untouched**: Generated OpenAPI and protobuf outputs should remain transport-agnostic. UDS belongs in the package-maintained wrapper layers.
3. **TCP remains the default**: Existing apps should not need any migration work unless they intentionally switch to UDS.
4. **Local-agent orientation**: This enhancement targets same-machine agent/service deployments. Browser and remote-access use cases remain TCP/HTTP concerns.
5. **Docs must prove the local story**: The primary docs examples should use the simplest self-contained local stack: SQLite datastore, SQLite vector backend, local cache, and UDS listener.
6. **Tutorial continuity over novelty**: Framework-specific UDS pages should continue from the response-resumption checkpoint for each stack so the documented change is “switch transport to UDS,” not “introduce a separate sample app.”

## Security Considerations

1. Client packages should require absolute UDS paths to avoid ambiguous working-directory behavior.
2. Logs should avoid printing bearer tokens or other sensitive headers while still surfacing the chosen transport mode and socket path when useful.
3. Documentation should continue to recommend filesystem permissions as the primary access-control boundary for local UDS deployments.

## Verification

```bash
# Spring modules
./java/mvnw -f java/pom.xml compile -pl spring/memory-service-rest-spring,spring/memory-service-proto-spring,spring/memory-service-spring-boot-autoconfigure -am

# Quarkus runtime modules
./java/mvnw -f java/pom.xml compile -pl quarkus/memory-service-extension -am

# Python package verification
python3 -m compileall python/langchain/memory_service_langchain python/langgraph/memory_service_langgraph

# TypeScript package build
cd typescript/vercelai && npm run build

# Site/docs validation if docs and checkpoints are updated
task test:site > site.log 2>&1
# Search for failures using Grep tool on site.log
```
