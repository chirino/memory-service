# Purpose of the Project

This project aims to implement a memory service for AI agaents.  At a highlevel it will store all the messaages
the agent sent and received to/from all the actors the Agent interacts with such as the LLMs and users.  It will support
the ability to replay all these messages in the order that they occured in the converstation.  Furthermore it will support
forking a converstartion at any given message.

## Use Access Control

Conerstartions will be owner by a user.  Users should only be able to access thier own converstations.  Converstations should be able to be shared with other users (users will have read, write, manager, owner access).  Ownership of a converstation can be transfered ot another user if he accepts the transfer.  Every conversation only has one owner at a time.

## Memory Service APIs

This memory service will expose APIs for slighlty different needs

### User Focused APIs

This API is meant to be used by chat frontend application.  It will needs to:

* list previous converstations
* do semantic search accross all converstations the user has access to.
* get all the messages of a converstation between the users in the converstation and the agent.
* fork converstations

### Agent Focused API

This API is meant to be used by the agent to retrieve the previous messages needed to build the context needed to be sent to the LLM.
The agent may compress or prevously seen messages by sumarizing them.  These sumerizations should be stored in the converstation history, but not displayed to the end user.  An agent will not be interested in messages that occured before the sumerization.

## Data stores

The project will support storing the the conversatations in multiple types of data stores like:
* PostgreSQL
* MongoDB

It will support multiple types of caches like:
* Redis
* Infinispan

It will support semantic search across all preivous converstations for a user by also storing converstions in a vector store store suceh as:
* PGVector
* Infinispan
* MongoDB

# Repository Guidelines

## Project Overview
`memory-service` is a multi-module Maven project built on Quarkus (Java 21). Maven Wrapper (`./mvnw`) is the supported build entrypoint.

## Project Structure & Module Organization
- Root:
  - `pom.xml`: parent POM, shared build config.
  - Modules:
    - `memory-service-contracts`: OpenAPI + proto sources of truth (no generated code).
    - `quarkus`: parent aggregator for Quarkus-facing modules.
    - `spring`: parent aggregator for upcoming Spring artifacts (currently empty skeleton).
    - `examples`: parent for shared examples (SPA lives here; the Quarkus agent is built via the `quarkus` aggregator).

- `quarkus/`:
  - `memory-service-rest-quarkus/`: Generated Quarkus REST client and helpers (consumes the shared contract).
    - Spec path (source of truth): `memory-service-contracts/src/main/resources/openapi.yml`.
  - `memory-service-proto-quarkus/`: generated gRPC stubs targeting Quarkus.
  - `memory-service/`: core memory-service HTTP + gRPC implementation.
  - `memory-service-extension/`: Quarkus extension (runtime + deployment) that wires dev services and client config.
  - `quarkus-data-encryption/`: data-encryption extension family (`runtime`, `deployment`, `quarkus-data-encryption-dek`, `quarkus-data-encryption-vault`).
  - `../examples/agent-quarkus`: LangChain4j-based agent app (included in the `quarkus` reactor).

- `examples/`:
  - `agent-quarkus/`: Quarkus agent example (depends on the REST client + extension).
    - Sources: `examples/agent-quarkus/src/main/java/example/` etc.
- `examples/agent-webui/`: frontend SPA (React + Vite + TypeScript + Tailwind CSS) shared by agents.

- `spring/`:
  - `spring/pom.xml`: placeholder aggregator for Spring REST/proto/starter modules to be added in later steps.

### Frontend (Web UI)
- Location: `examples/agent-webui/`.
- Framework/tooling: React 19, React DOM 19, Vite, TypeScript, Tailwind CSS, Radix UI primitives, Lucide icons.
- `npm install` (or `pnpm`/`yarn` as preferred) should be run from `examples/agent-webui/` before frontend dev/build tasks.
- Frontend scripts (from `examples/agent-webui/package.json`):
  - `npm run dev`: run Vite dev server.
  - `npm run build`: type-check and build the frontend bundle.
  - `npm run lint`: run ESLint.
  - `npm run preview`: preview the built frontend.
- Authentication:
- The SPA talks to the backend REST APIs (for example, `/v1/user/conversations`) via the agent app, using the generated OpenAPI client under `examples/agent-webui/src/client/` (or equivalent client code).
  - User-facing endpoints are protected by Quarkus OIDC. When a call fails due to lack of auth (401 or a dev OIDC redirect/CORS failure), the landing page shows a “Sign in” prompt instead of data.
  - The “Sign in” button performs a full page navigation to the backend login helper endpoint (currently `/auth/login`), which is annotated with `@Authenticated`. In web-app mode, this triggers the OIDC login redirect to Keycloak (Dev Services or docker-compose Keycloak) and, after successful login, sends the user back to the SPA, where requests now carry the authenticated session.
  - The OpenAPI client is configured to send credentials (`WITH_CREDENTIALS=true`) so session cookies from the OIDC login are included on subsequent API calls.

- UI components:
- When adding or extending UI, prefer using base components from https://ui.shadcn.com/ and follow the existing patterns under `examples/agent-webui/src/components/ui/` (for example, `button`, `card`) instead of hand-rolling new primitives.
  - New UI primitives should generally be introduced by adapting shadcn/ui components into this local `ui` library, then composed from there.

### Example consumer (LangChain4j)
- Location: `examples/agent-quarkus/src/main/java/example/`.
- Purpose: simulate a downstream agent/chat application that consumes the memory service.
  - `Agent` (`example/Agent.java`) is a LangChain4j AI service that exposes a `chat(@MemoryId String memoryId, String userMessage)` API.
  - `AgentWebSocket` (`example/AgentWebSocket.java`) exposes a WebSocket endpoint for streaming agent responses.
  - `ResumeWebSocket` (`example/ResumeWebSocket.java`) exposes a WebSocket endpoint for resuming conversations from a specific position.
  - `HistoryRecordingAgent` (`example/HistoryRecordingAgent.java`) wraps the agent to record conversation history.
  - `MemoryServiceProxyResource` (`example/MemoryServiceProxyResource.java`) proxies memory-service user-facing APIs to the frontend.
  - `ResumeResource` (`example/ResumeResource.java`) provides an endpoint to check if a conversation can be resumed.
  - `SummerizationResource` (`example/SummerizationResource.java`) provides conversation summarization with redaction support.
  - `RedactionAssistant` (`example/RedactionAssistant.java`) is a LangChain4j AI service for identifying sensitive information to redact.
- Integration with this memory-service:
  - The example frontend uses the memory-service's user-facing APIs (proxied via `MemoryServiceProxyResource`) to list past conversations and continue existing ones.
  - The LangChain4j `Memory` implementation (`MemoryServiceChatMemory` and friends) lives under `memory-service/src/main/java/io/github/chirino/memory/langchain4j/` and interacts with the service's `/v1/agent/*` and `/v1/user/*` APIs.

## Build, Test, and Development Commands
- `./mvnw quarkus:dev -pl memory-service`: run the memory-service backend in dev mode with live reload (Dev UI at `http://localhost:8080/q/dev/`).
- `./mvnw quarkus:dev -pl examples/agent-quarkus`: run the agent app + SPA in dev mode (typically on `http://localhost:8081`).
  - In these modes, Quarkus Dev Services will spin up development dependencies automatically (e.g., databases, caches) as configured.
  - The `memory-service-extension` automatically starts the memory-service in a Docker container and configures `MEMORY_SERVICE_URL`, `memory-service.url`, and `quarkus.rest-client.openapi_yml.url` to point to the running container.
  - The dev service will only start if Docker is available and the URLs are not explicitly configured via environment variables or system properties.
- `./mvnw test`: run JVM tests (Surefire).
- `./mvnw package`: build a runnable app under `target/quarkus-app/` (`java -jar target/quarkus-app/quarkus-run.jar`).
- `./mvnw package -Dquarkus.package.jar.type=uber-jar`: build an uber-jar (`java -jar target/*-runner.jar`).
- `./mvnw verify`: run full verification; integration tests are skipped by default (`skipITs=true`).
- `./mvnw verify -DskipITs=false`: include integration tests (Failsafe).
- `./mvnw package -Dnative` (or `-Dquarkus.native.container-build=true`): build a native executable.

**Tip**: When debugging build failures, use `tail -50` to get enough context from the build output. For example: `./mvnw test 2>&1 | tail -50` or `./mvnw compile 2>&1 | tail -50`.

### Production-like Local Deployment
- Use `docker compose up -d` to start a more production-like stack.
- The `service` service runs the memory-service backend image (`memory-service-service:latest`) and connects to:
  - `postgres` (PostgreSQL) for the main datastore and Keycloak DB.
  - `redis` for caching.
  - `mongodb` for MongoDB-based storage/vector store.
  - `keycloak` as the OIDC identity provider.
- The `agent` service runs the agent + SPA image (`memory-service-agent:latest`) and connects to:
  - `service` (via `MEMORY_SERVICE_URL`) for all memory-service APIs.
  - `keycloak` for OIDC.
- Quarkus is configured via environment variables in `docker-compose.yaml` so that it talks to these containers instead of Dev Services.

## Coding Style & Naming Conventions
- Java: 4-space indentation, UTF-8, keep imports organized; prefer constructor injection and small resource methods.
- Naming: packages are lower-case reverse-domain (e.g., `io.github.chirino`), classes `PascalCase`, methods/fields `camelCase`.

## Testing Guidelines
- Frameworks: JUnit 5 (`quarkus-junit5`) and RestAssured for HTTP assertions.
- Keep tests deterministic; prefer black-box HTTP tests against Quarkus test runtime.
- **Prefer Cucumber-based tests for REST and gRPC API testing**: When testing memory-service REST and gRPC interfaces, favor Cucumber feature files (located in `memory-service/src/test/resources/features/`) over unit tests with mocks. Cucumber tests provide:
  - Living documentation of API behavior
  - End-to-end integration testing through the full API boundary
  - Better readability for non-technical stakeholders
  - Consistent test patterns across the codebase
- Unit tests with mocks should be reserved for:
  - Testing internal implementation details and business logic
  - Testing infrastructure/configuration (e.g., Liquibase migrations, selector logic)
  - Testing test framework behavior itself
- **Cucumber Test Failure Reporting**: When memory-service Cucumber tests fail, check `memory-service/target/cucumber/failures.txt` for a structured, failure-only summary. This file contains:
  - Feature and scenario names for failed tests
  - Failed step details with full error messages and stack traces
  - Machine-readable JSON output in `memory-service/target/cucumber/failures.json`
  - The failures files are automatically generated after test execution and deleted at the start of each test run to ensure a clean state

## Commit & Pull Request Guidelines
- Git history may not be available in this checkout; use clear, imperative commit subjects (or Conventional Commits like `feat:`, `fix:`, `docs:`).
- PRs: include a short summary, how you tested (`./mvnw test`, `./mvnw verify -DskipITs=false`), and any config changes (`application.properties`).

## Security & Configuration Tips
- Don’t commit secrets; prefer environment variables or Quarkus config (`QUARKUS_*`) overrides.
- If adding new endpoints, consider auth, input validation, and logging hygiene.

## OpenAPI Spec Changes

When you change the OpenAPI contract (conversation endpoints, schemas, etc.), keep Java and frontend clients in sync:

- Update the spec under `memory-service-contracts/src/main/resources/openapi.yml` (this is the source of truth for generation).
- Regenerate the Java client and ensure it compiles (from the project root):
  - `./mvnw -pl quarkus/memory-service-rest-quarkus clean compile`
- Regenerate the frontend TypeScript client:
- From `examples/agent-webui/`, run `npm install` (once) and then `npm run generate`.
- Update any application code to use renamed paths, operations, or types (Java REST resources, LangChain4j integration, and React code using `examples/agent-webui/src/client`).

## Notes for AI Assistants

- **ALWAYS run a linter or compile after making changes to typesafe code**: After editing any typesafe code (Java, TypeScript, etc.), you MUST verify that your changes compile and pass linting checks:
  - For Java code: Run `./mvnw compile` (or `./mvnw compile -pl <module>` for a specific module) to ensure the code compiles without errors.
- For TypeScript/JavaScript frontend code: Run `npm run lint` and/or `npm run build` from `examples/agent-webui/` to check for linting errors and type errors.
  - For OpenAPI spec changes: Follow the regeneration steps in the "OpenAPI Spec Changes" section and verify compilation.
  - Do not skip this step; it catches type errors, syntax issues, and integration problems early.

- Data encryption is provided by the `quarkus-data-encryption` extension under `quarkus-data-encryption/` (runtime + deployment) with optional providers (`dek`, `vault`) in submodules.
- The `plain` provider is always available and acts as a no-op (identity) encrypt/decrypt. If `data.encryption.providers` is not set, `DataEncryptionService` defaults to using only `plain`.
- The DEK provider (`DekDataEncryptionProvider`) now requires `data.encryption.dek.key` **only** when `"dek"` appears in `data.encryption.providers`; if `dek` is not listed, the provider skips key initialization and is effectively disabled. This allows dev and tests to run with plain-only encryption without extra config.
- The memory-service’s default dev configuration already uses the plain provider (`memory-service/src/main/resources/application.properties`):
  - `data.encryption.providers=plain`
  - `data.encryption.provider.plain.type=plain`
- The `memory-service-extension` dev service (`memory-service-extension/deployment/src/main/java/io/github/chirino/memory/deployment/DevServicesMemoryServiceProcessor.java`) starts a `memory-service-service:latest` container when running the `agent` module in dev mode:
  - It is skipped if `MEMORY_SERVICE_URL` or `memory-service.url` are set.
  - It rewrites JDBC and OIDC URLs from `localhost` to `host.docker.internal` so the container can reach Dev Services (Postgres, Keycloak) started by the agent.
  - It wires `MEMORY_SERVICE_URL`, `memory-service.url`, and `quarkus.rest-client.memory-service-client.url` for the agent to talk to the dev-service container.


## Develoment Lifecylce

We are still in the initial development of this project.  It has not yet been released as a supported project.
Your changes do not need to be backward compataible.  We don't deprecate, we are working towards our first release.
