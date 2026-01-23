# Spring Boot support: design and implementation plan

## Goals
- Provide first-class Spring Boot equivalents for the REST client, gRPC/proto artifacts, dev-time helpers, and the example agent.
- Keep Quarkus support intact while allowing a future rename of the REST client module.
- Offer a consistent naming scheme so REST vs gRPC artifacts are obvious, and Quarkus vs Spring Boot variants are discoverable.

## Naming and module layout proposal
- Keep `memory-service/` at the root as the primary deliverable.
- Spec-only module is already in place as `memory-service-contracts` (OpenAPI + proto definitions).
- Add two parent aggregators (still to do):
  - `quarkus/` parent containing Quarkus-facing modules:
    - `memory-service-rest-quarkus` (rename of current `memory-service-client`).
    - `memory-service-proto-quarkus` (current proto codegen behavior).
    - `memory-service-extension` (Quarkus extension).
    - `quarkus-data-encryption/*` moved under this parent.
    - `examples/agent-quarkus` (current `agent`).
  - `spring/` parent containing Spring-facing modules:
    - `memory-service-rest-spring` (generated client + support).
    - `memory-service-proto-spring` (Spring-friendly gRPC stubs/helpers).
    - `memory-service-spring-boot-autoconfigure` and `memory-service-spring-boot-starter`.
    - `examples/agent-spring` (Spring Boot agent example).
- Keep the SPA as `examples/agent-webui` (shared by both agents).

## REST client (Spring Boot) plan
- Generation: use OpenAPI Generator `spring` client with WebClient (reactive) as the default transport; enable `useSpringBoot3`, `dateLibrary=java8`, and `useOptional`.
- Auth: support both API key (`X-API-Key`) and OIDC bearer. For bearer, wire to Spring Security’s `OAuth2AuthorizedClientManager` (client credentials and on-behalf-of) with a pluggable interceptor.
- Configuration properties (prefix `memory-service.client.*`):
  - `base-url`, `api-key`, `oidc.client-registration`, `timeout`, `log-requests`, `with-credentials` equivalent.
- Packaging:
  - `memory-service-rest-spring`: generated sources + a small “support” package (filters/interceptors, config properties, builders).
  - Tests: slice tests verifying auth header propagation and base URL override.

## REST client rename/migration
- Publish `memory-service-rest-quarkus` with the same code as today’s `memory-service-client`.
- Update docs and internal references (`memory-service-extension`, agent) to the new artifactId once the rename lands.

## Proto/gRPC plan
- Move `.proto` files to `memory-service-contracts/src/main/proto` (source of truth alongside OpenAPI under `src/main/openapi`).
- Point `memory-service-proto-quarkus` codegen at `memory-service-contracts`.
- Add `memory-service-proto-spring` using `protobuf-maven-plugin` + `grpc-java` with `grpc-spring-boot-starter` compatibility (netty transport). Provide a small `MemoryServiceGrpcClients` helper to build stubs from Spring config (`memory-service.grpc.*`).
- Validate generated packages stay the same so both runtimes interoperate.

## Spring Boot starter
- Module: `memory-service-spring-boot-autoconfigure` (code) + `memory-service-spring-boot-starter` (brings autoconfigure + `memory-service-rest-spring` + optional proto client).
- Auto-configure:
  - REST client bean (`MemoryServiceClients` builder) honoring properties and plugging in API key / OAuth2.
  - Optional gRPC channel/stubs when `memory-service.grpc.enabled=true`.
  - Metrics/logging toggles that mirror the Quarkus filters.
- Provide conditional beans so users can override WebClient/RestTemplate or supply their own security.
- Include sample `application.yml` snippets in README.

## Spring Boot agent example
- Module: `agent-spring` (Spring Boot 3, Java 21).
- Responsibilities:
  - Expose the same proxy endpoints as the Quarkus agent (`/v1/user/*`), SSE/WebSocket streaming, and summarization hooks.
  - Use `memory-service-rest-spring` (and optional gRPC) clients.
  - Serve the existing SPA build from `agent-webui` (static resources), or proxy Vite dev server in dev profile.
- Tests: minimal WebTestClient or MockMvc smoke tests for auth and proxy wiring.

## Execution phases
1) **Module skeletons and naming** *(mostly done)*
   - ✅ Create `memory-service-contracts` (OpenAPI + proto only, no generated code).
   - ✅ Reorganize modules under `quarkus/` and `spring/` parents, rename `memory-service-client` -> `memory-service-rest-quarkus`, move `quarkus-data-encryption` under `quarkus/`, and relocate examples under `examples/`.
   - ✅ Rebuild and run validation tests (initial compile run) to confirm the refactor is stable before adding any new Spring modules.
2) **Spring REST client**
   - Add generator config and support code; verify compile and publishable POM.
3) **Spring proto client**
   - Wire protobuf/grpc plugin, add helper/builder, basic tests.
4) **Spring Boot starter**
   - Implement autoconfigure + starter; document properties; tests for property binding and auth header propagation.
5) **Agent Spring**
   - Build feature-parity proxy and streaming; hook SPA assets; add smoke tests under `examples/agent-spring`.
6) **Docs and migration**
   - Update README, module references, and release notes; deprecate old artifactId references.
   - Run a rebuild + user acceptance pass after the module reorg and before creating the new Spring modules; repeat after Spring additions.

## Open decisions
- Rename `memory-service-client` immediately; no legacy artifact is needed.
- Use reactive `WebClient` as the default for the Spring REST client.
- Bidirectional gRPC streaming is required for the Spring example.
