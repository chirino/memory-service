# gRPC API Design for memory-service

## Goals
- Expose all memory-service REST endpoints as gRPC endpoints while preserving behavior and access control.
- Keep REST and gRPC implementations aligned to a single service layer to avoid divergence.
- Provide a clear evolution path for clients (Java + agent + future integrations) with stable proto contracts.
- Follow Quarkus gRPC implementation guidance (proto in `src/main/proto`, `@GrpcService`, Mutiny API).

## Non-goals
- Replacing the REST API or removing existing REST endpoints.
- Designing a public gRPC API for browser consumption (gRPC-Web is out of scope for initial phase).
- Changing authorization semantics or introducing cross-tenant access.

## Constraints and Quarkus gRPC considerations
- gRPC service code is generated from proto files placed in `src/main/proto` and generated during `mvn compile`.
- gRPC services must be CDI beans annotated with `@GrpcService` and should not use additional qualifiers.
- By default, gRPC methods run on the event loop; blocking operations require `@Blocking`.
- gRPC health, reflection, and metrics can be enabled via Quarkus configuration.
- Authentication/authorization for gRPC uses Quarkus Security; for shared HTTP port with REST, set `quarkus.grpc.server.use-separate-server=false`.

## Current REST scope to mirror
REST endpoints are defined in the OpenAPI spec: `memory-service-contracts/src/main/resources/openapi.yml`.
The gRPC services must cover all operations in that spec (user APIs and agent APIs).

### Proto source of truth
Proto files live under `memory-service-contracts/src/main/proto` next to the OpenAPI spec so both REST and gRPC contracts are owned by the shared contracts module. The contracts module publishes those `.proto` files as resources only (no stub generation). The `memory-service-proto-quarkus` module runs the Quarkus gRPC generator once and packages the generated classes, which the `memory-service` server and `memory-service-extension` runtime now consume directly instead of regenerating them. Keeping a single proto tree in `memory-service-contracts` avoids duplication and ensures every module consumes the same schema while each module uses the shared generated stubs appropriate for its role.

## Design overview

### Service layout
Map REST API groups to the following Quarkus gRPC services for readability and compatibility:
- `SystemService` (health)
- `ConversationsService` (conversation CRUD, forks)
- `ConversationMembershipsService` (sharing, memberships, ownership)
- `MessagesService` (paginated message listing plus agent-only append)
- `SearchService` (vector search plus `CreateSummary`/agent summaries)

Every service is implemented as a CDI bean annotated with `@GrpcService` and delegates to the existing business/service layer. This keeps REST and gRPC behavior consistent while isolating transport-specific details in the gRPC boundary.

### Proto and message design
- Define a `memory/v1` proto package and versioned Java package (e.g., `io.github.chirino.memory.grpc.v1`).
- Use explicit request/response messages for each endpoint, even for simple RPCs.
- Use snake_case in proto fields, with `json_name` if needed for compatibility with existing JSON naming.
- Use standard wrapper types for optional scalars where useful.

### REST-to-gRPC mapping
- `GET` list endpoints -> unary `List*` RPCs returning repeated items plus paging metadata.
- `GET` single resource -> unary `Get*` RPC.
- `POST` create -> unary `Create*` RPC returning created resource.
- `POST` action (fork/resume/summarize) -> unary `*Action` RPC.
- `DELETE` -> unary `Delete*` RPC.

### Pagination and filtering
- Create a shared `PageRequest { string page_token; int32 page_size; }` and `PageInfo { string next_page_token; }`.
- For endpoints currently using offset/limit, translate to `page_token` encoding offset (opaque). Keep legacy offset values in the internal service layer to avoid refactors.

### Error handling
- Map REST error responses to gRPC status codes:
  - 400 -> `INVALID_ARGUMENT`
  - 401 -> `UNAUTHENTICATED`
  - 403 -> `PERMISSION_DENIED`
  - 404 -> `NOT_FOUND`
  - 409 -> `ALREADY_EXISTS` or `FAILED_PRECONDITION`
  - 422 -> `FAILED_PRECONDITION`
  - 500 -> `INTERNAL`
- Include structured error details using `google.rpc.Status` if needed, but keep MVP to status + message.

### Streaming
Most endpoints are unary. If any endpoint returns large message lists, consider a server-streaming variant (e.g., `ListMessagesStream`) later. Keep initial API unary for parity with REST.

## Security model
- Preserve existing ownership and shared access rules (read/write/manage/owner).
- Require authentication for all gRPC methods that mirror REST user/agent APIs.
- Preferred auth mechanism: Bearer tokens (OIDC) sent via gRPC metadata header `Authorization: Bearer ...`.
- If sharing HTTP server (same port) with REST, enable `quarkus.grpc.server.use-separate-server=false` to reuse HTTP security config.
- Note: session cookies from the browser are not a good fit for gRPC clients; prioritize token-based auth for gRPC.

## Observability and ops
- Enable gRPC health and reflection for easier debugging (dev and test); consider disabling reflection in production if needed.
- Ensure gRPC metrics are enabled if existing Micrometer setup is expected.
- Add structured logging for gRPC requests, with correlation IDs if already present in REST.

## Compatibility and versioning
- Use `memory.v1` namespace; any breaking changes create `memory.v2`.
- Keep REST and gRPC schemas aligned by generating proto from OpenAPI as a future enhancement (not required for initial phase).

## Implementation plan

### Phase 1: Foundation
1. Add Quarkus gRPC server dependencies to `memory-service` module.
2. Add proto sources to `memory-service-contracts/src/main/proto/memory/v1/*.proto` to mirror OpenAPI.
3. Configure `memory-service-contracts` to publish proto files as resources (no stub generation).
4. Configure gRPC server stub generation in `memory-service` by scanning the proto dependency via `quarkus.generate-code.grpc.scan-for-proto`.
5. Configure gRPC server in `memory-service/src/main/resources/application.properties`.
6. Implement gRPC services using Mutiny API and `@GrpcService`.
7. Wire gRPC implementations to existing service layer.

### Phase 2: Auth and access control
1. Integrate Quarkus Security for gRPC auth using `Authorization` metadata.
2. Ensure existing RBAC rules apply to gRPC flows.
3. Add gRPC authorization tests (mirroring REST tests).

### Phase 3: Client and tooling
1. Add Quarkus gRPC client dependencies to `memory-service-extension` module.
2. Configure gRPC client stub generation in `memory-service-extension` by scanning the proto dependency from `memory-service-contracts`.
3. Ensure `memory-service-contracts` publishes proto files as resources alongside REST (OpenAPI) definitions.
4. Add example usage in `examples/agent-quarkus` (gRPC client optional; REST remains default).
5. Document gRPC CLI usage for smoke testing.

### Phase 4: Operational hardening
1. Add metrics, health, reflection configs.
2. Add integration tests for gRPC endpoints.
3. Validate performance, including any blocking calls annotated with `@Blocking` or move to virtual threads if needed.

## Risks and open questions
- gRPC auth mechanism choice: token-based vs session cookie. Recommendation: token-based for gRPC clients.
- Event-loop blocking: current service layer may block on DB calls; gRPC methods must be marked `@Blocking` or use virtual threads configuration.
- Separate gRPC server vs shared HTTP server: sharing simplifies auth but couples ports and TLS configuration.
- Proto and OpenAPI drift: without generation, manual sync is required; consider OpenAPI-to-proto tooling later.
- gRPC-Web requirements for browser clients are out of scope for phase 1.
- DTO reuse between REST and gRPC: direct reuse is limited due to differing schemas and generated classes; define explicit mappers or introduce shared domain DTOs.

## Client module and DTO reuse

### Client module shape
The `memory-service-contracts` module publishes proto definitions as resources alongside REST (OpenAPI) definitions. The `memory-service-extension` module generates Quarkus gRPC client stubs from these proto files, providing a clean separation where the client module owns the contract definitions while the extension module provides the generated client implementation. This keeps the client module lightweight (proto files only) while allowing the extension to manage gRPC client dependencies and generation.

### Can we reuse REST DTOs for gRPC?
Short answer: not directly. REST DTOs are Java classes generated from OpenAPI, while gRPC messages are `protoc`-generated classes with different types and builders. They are not wire-compatible or type-compatible.

Recommended approaches:
1. **Explicit mapping layer**: Create translators between REST DTOs and gRPC messages in a small shared mapping module.
2. **Shared internal domain DTOs**: Move core data models into a shared `memory-service-model` module, and map both REST and gRPC representations to/from these internal DTOs.
3. **Proto-first models**: Define gRPC messages as the source of truth and generate REST DTOs from proto via a toolchain (more complex; not required for v1).

Given the current OpenAPI-first flow, option 1 is the least disruptive and keeps the API contracts independent while reducing duplication through mappers.

## Mapping strategy (REST + gRPC share domain DTOs)

### Principles
- REST and gRPC endpoints use the same domain DTOs for business logic.
- gRPC is transport-only; it maps to/from domain DTOs at the boundary.
- Mapping is compile-time safe and native-image friendly; no reflection.
- Mapping logic lives in dedicated mapper classes, not REST or gRPC services.

### MapStruct requirements
- Use MapStruct with `@Mapper(componentModel = "jakarta")` so mappers are CDI beans.
- Prefer explicit `@Mapping` definitions for each field.
- Fail the build on unmapped fields with `unmappedTargetPolicy = ReportingPolicy.ERROR`.
- Protobuf classes are immutable and use builders; MapStruct should target the builder API.

### Suggested module layout
- `memory-service-model`: domain DTOs used by business logic (shared by REST and gRPC).
- `memory-service-mapping`: MapStruct mappers between domain DTOs and REST DTOs and/or gRPC messages.
- `memory-service`: REST resources and gRPC services depend on mappers and domain DTOs, but contain no mapping logic.

### Example mapper configuration (shared)
```java
import org.mapstruct.Mapper;
import org.mapstruct.ReportingPolicy;

@Mapper(
    componentModel = "jakarta",
    unmappedTargetPolicy = ReportingPolicy.ERROR
)
public interface GrpcConversationMapper {
    // explicit @Mapping declarations go here
}
```

### Protobuf builder mapping notes
- MapStruct can map to builder types; configure mappings to target `Message.Builder` and then call `build()` in a default method if needed.
- Prefer explicit mappings for nested messages and repeated fields to avoid surprises.

## Proposed configuration (initial)
```
# Run gRPC on same server as REST to reuse HTTP security (optional)
quarkus.grpc.server.use-separate-server=false
```

## Deliverables
- Proto files under `memory-service-contracts/src/main/proto/memory/v1/` (published as resources only).
- gRPC server stub generation configured in `memory-service` module.
- gRPC service implementations in `memory-service/src/main/java/io/github/chirino/memory/grpc/`.
- gRPC client stub generation configured in `memory-service-extension` module.
- Updated Quarkus configuration in `memory-service/src/main/resources/application.properties`.
- Tests under `memory-service/src/test/java` for key gRPC endpoints.
