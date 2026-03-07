---
status: implemented
---

# Enhancement 070: Standardize Client API Names to ResponseRecordingManager and RecordingSession

> **Status**: Implemented.

## Summary

Standardize the client-side response recording/resumption API names across Spring, Quarkus, Python, site docs, and example apps by renaming `ResponseResumer` to `ResponseRecordingManager` and nested `ResponseRecorder` to `RecordingSession`.

## Motivation

Current naming under-describes the API surface and causes confusion in examples and docs:

- `ResponseResumer` implies replay-only behavior, but the API also starts recording sessions, checks active recordings, reports enablement, and handles cancellation.
- Nested `ResponseRecorder` sounds like a standalone service client rather than a per-conversation session handle.
- Python and Java stacks use mixed terms (`MemoryServiceResponseResumer`, `ResponseResumer`, `ResponseRecorder`) for the same lifecycle concept.

A unified naming model improves discoverability and aligns the type names with actual responsibilities.

## Design

### 1. Naming Standard

Adopt these canonical names for client-side integrations:

| Current name | New name |
|---|---|
| `ResponseResumer` | `ResponseRecordingManager` |
| `ResponseResumer.ResponseRecorder` | `ResponseRecordingManager.RecordingSession` |
| `MemoryServiceResponseResumer` (Python) | `MemoryServiceResponseRecordingManager` |

Apply this standard to code, imports, bean names, variables, examples, and docs.

### 2. Spring Client Library Changes

Rename and update the `history` API surface in `spring/memory-service-spring-boot-autoconfigure`:

- `ResponseResumer` interface file/type -> `ResponseRecordingManager`
- Nested `ResponseRecorder` -> `RecordingSession`
- `GrpcResponseResumer` -> `GrpcResponseRecordingManager`
- `NoopResponseResumer` -> `NoopResponseRecordingManager`
- Injection points and bean methods updated accordingly (including auto-configuration method names)

No compatibility shim is required (pre-release policy).

### 3. Quarkus Extension Changes

Rename and update the `history/runtime` API surface in `quarkus/memory-service-extension`:

- `ResponseResumer` interface file/type -> `ResponseRecordingManager`
- Nested `ResponseRecorder` -> `RecordingSession`
- `GrpcResponseResumer` -> `GrpcResponseRecordingManager`
- `NoopResponseResumer` -> `NoopResponseRecordingManager`
- `ResponseResumerProducer` -> `ResponseRecordingManagerProducer`
- All adapter and store injection points migrated to new type names

No compatibility shim is required (pre-release policy).

### 4. Python Library Changes

Rename the public helper and update exports in `python/langchain/memory_service_langchain`:

- `MemoryServiceResponseResumer` -> `MemoryServiceResponseRecordingManager`
- Module filename `response_resumer.py` -> `response_recording_manager.py`
- `__init__.py` export list and docs updated
- Example imports and variable names updated (`resumer` -> `recording_manager`)

Generated gRPC types under `grpc/memory/v1` are unchanged.

### 5. Examples and Site Docs

Update all Spring/Quarkus/Python examples and docs to consistently use the new names:

- Example source files: `spring/examples/**`, `quarkus/examples/**`, `python/examples/**`
- Site docs pages under:
  - `site/src/pages/docs/spring/**`
  - `site/src/pages/docs/quarkus/**`
  - `site/src/pages/docs/python-langchain/**`
  - `site/src/pages/docs/python-langgraph/**`
  - `site/src/pages/docs/concepts/**` and shared config pages where "response resumer" appears

This includes prose, code snippets, import blocks, and explanatory text.

### 6. Scope Boundaries

In scope:

- Client-side library naming and API references in Spring, Quarkus, Python
- Example applications and doc checkpoints
- Site documentation terminology and code snippets

Out of scope:

- gRPC contract service name `ResponseRecorderService` in `memory_service.proto`
- Server-side Go/Java recorder RPC behavior changes

## Testing

### Automated Checks

- Spring module compile after rename propagation.
- Quarkus module compile after rename propagation.
- Python module compile checks and example import validation.
- Site documentation tests to confirm updated snippets remain executable.

### BDD Coverage (Gherkin)

```gherkin
Feature: Response recording manager naming across examples

  Scenario: Spring and Quarkus examples compile with new manager/session names
    Given the client API type is named ResponseRecordingManager
    When I build the spring and quarkus example checkpoints
    Then compilation should succeed without ResponseResumer symbols

  Scenario: Python examples import new response recording manager helper
    Given the Python helper class is named MemoryServiceResponseRecordingManager
    When I run Python compile checks for langchain and langgraph examples
    Then imports should resolve without MemoryServiceResponseResumer references

  Scenario: Site docs use standardized naming
    Given site docs include response resumption guides
    When I run site docs tests
    Then snippets and prose should reference ResponseRecordingManager or MemoryServiceResponseRecordingManager
```

## Tasks

- [x] Rename Spring `ResponseResumer` API types/files to `ResponseRecordingManager` and `RecordingSession`.
- [x] Rename Quarkus `ResponseResumer` API types/files to `ResponseRecordingManager` and `RecordingSession`.
- [x] Rename Python `MemoryServiceResponseResumer` module/class/export to `MemoryServiceResponseRecordingManager`.
- [x] Update all Spring/Quarkus/Python examples and doc-checkpoint code to new names.
- [x] Update all affected site docs/snippets/prose to new names.
- [x] Run targeted compile/tests for changed modules and site docs tests.
- [x] Update module `FACTS.md` entries after implementation to reflect final canonical names.

## Files to Modify

| File(s) | Planned change |
|---|---|
| `spring/memory-service-spring-boot-autoconfigure/src/main/java/io/github/chirino/memoryservice/history/ResponseResumer.java` | Rename interface/type to `ResponseRecordingManager`; nested `RecordingSession` |
| `spring/memory-service-spring-boot-autoconfigure/src/main/java/io/github/chirino/memoryservice/history/GrpcResponseResumer.java` | Rename class/type usage to `GrpcResponseRecordingManager` |
| `spring/memory-service-spring-boot-autoconfigure/src/main/java/io/github/chirino/memoryservice/history/NoopResponseResumer.java` | Rename class/type usage to `NoopResponseRecordingManager` |
| `spring/memory-service-spring-boot-autoconfigure/src/main/java/io/github/chirino/memoryservice/history/ConversationHistoryAutoConfiguration.java` | Update bean signatures/names and injected types |
| `spring/memory-service-spring-boot-autoconfigure/src/main/java/io/github/chirino/memoryservice/history/ConversationHistoryStreamAdvisor.java` | Update manager/session type references |
| `spring/examples/**` | Update imports, constructor params, and variable names in examples/checkpoints |
| `quarkus/memory-service-extension/runtime/src/main/java/io/github/chirino/memory/history/runtime/ResponseResumer.java` | Rename interface/type to `ResponseRecordingManager`; nested `RecordingSession` |
| `quarkus/memory-service-extension/runtime/src/main/java/io/github/chirino/memory/history/runtime/GrpcResponseResumer.java` | Rename to `GrpcResponseRecordingManager`; update type references |
| `quarkus/memory-service-extension/runtime/src/main/java/io/github/chirino/memory/history/runtime/NoopResponseResumer.java` | Rename to `NoopResponseRecordingManager` |
| `quarkus/memory-service-extension/runtime/src/main/java/io/github/chirino/memory/history/runtime/ResponseResumerProducer.java` | Rename producer and injected type references |
| `quarkus/memory-service-extension/runtime/src/main/java/io/github/chirino/memory/history/runtime/ConversationStreamAdapter.java` | Update manager/session type references |
| `quarkus/memory-service-extension/runtime/src/main/java/io/github/chirino/memory/history/runtime/ConversationEventStreamAdapter.java` | Update manager/session type references |
| `quarkus/examples/**` | Update imports/injection and variable names in examples/checkpoints |
| `python/langchain/memory_service_langchain/response_resumer.py` | Rename module/class to `response_recording_manager.py` / `MemoryServiceResponseRecordingManager` |
| `python/langchain/memory_service_langchain/__init__.py` | Update exported symbol names |
| `python/langchain/README.md` | Update helper class references |
| `python/examples/**` | Update imports and variable names in examples/checkpoints |
| `site/src/pages/docs/spring/**` | Replace `ResponseResumer` references in prose/snippets |
| `site/src/pages/docs/quarkus/**` | Replace `ResponseResumer` references in prose/snippets |
| `site/src/pages/docs/python-langchain/**` | Replace `MemoryServiceResponseResumer` references |
| `site/src/pages/docs/python-langgraph/**` | Replace `MemoryServiceResponseResumer` references |
| `site/src/pages/docs/concepts/**` and `site/src/pages/docs/configuration.mdx` | Replace generic "response resumer" terminology |
| `spring/FACTS.md`, `quarkus/FACTS.md`, `python/FACTS.md` | Update canonical naming facts after implementation |

## Verification

```bash
# Spring (targeted compile)
./mvnw -pl spring/memory-service-spring-boot-autoconfigure compile

# Quarkus (targeted compile)
./mvnw -pl quarkus/memory-service-extension compile

# Python (compile changed package + examples)
python3 -m compileall python/langchain/memory_service_langchain python/examples

# Site docs tests (opens services; run in devcontainer worktree context)
wt up
wt exec -- go test -tags=site_tests ./internal/sitebdd/ -run TestSiteDocs -count=1
```

Implementation run notes:

- `spring/examples/chat-spring` required `-am` to resolve in-repo dependencies (`chat-frontend`, Spring starters) in a clean container.

## Non-Goals

- Renaming proto/gRPC service `ResponseRecorderService`.
- Changing response recorder protocol or semantics (`Record`, `Replay`, `Cancel`, `CheckRecordings`, `IsEnabled`).
- Behavior changes to stream buffering, replay framing, or cancel propagation.

## Design Decisions

- Use `Manager + Session` terminology to separate lifecycle controller responsibilities (`ResponseRecordingManager`) from per-conversation write handle responsibilities (`RecordingSession`).
- Keep "ResponseRecorder" naming at gRPC service level to preserve existing contract language and avoid contract churn.
- Keep existing `/response-resumption/` doc route slugs for link stability while updating visible page/sidebar terminology to "Response Recording and Resumption".
