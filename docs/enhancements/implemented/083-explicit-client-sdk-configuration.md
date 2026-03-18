---
status: implemented
---

# Enhancement 083: Explicit Client SDK Configuration

> **Status**: Implemented.

## Summary

The Python LangChain/LangGraph packages and the TypeScript Vercel AI package now use explicit constructor or helper configuration by default. Environment-variable resolution moved into opt-in factory helpers: Python uses `from_env()`, and TypeScript uses `memoryServiceConfigFromEnv()`.

## Motivation

The SDKs were reading `MEMORY_SERVICE_*` variables directly inside constructors and helper creation paths, which hid connection settings from the type system and made multi-instance and test setups awkward. They also silently fell back to `agent-api-key-1`, which masked missing configuration.

## Design

### Python LangChain

- `MemoryServiceCheckpointSaver`, `MemoryServiceHistoryMiddleware`, `MemoryServiceResponseRecordingManager`, and `MemoryServiceProxy` now take explicit config.
- Each class exposes `from_env(...)` for app-style env resolution.
- `transport.py` now owns env resolution via `resolve_env_config()`, while transport helpers are pure.
- `MemoryServiceProxy` now forwards `unix_socket` through the shared request helper so UDS works consistently for proxied REST calls.

### Python LangGraph

- `MemoryServiceStore` and `AsyncMemoryServiceStore` now take explicit `base_url` and `token`.
- Both classes expose `from_env(...)`.
- `transport.py` now resolves env config centrally and keeps REST transport helpers pure.

### TypeScript / Vercel AI

- Added exported `MemoryServiceConfig`.
- Added exported `memoryServiceConfigFromEnv(...)`.
- `createMemoryServiceProxy`, `withMemoryService`, `withProxy`, `memoryServiceResumeCheck`, `memoryServiceReplay`, and `memoryServiceCancel` now consume explicit config.
- gRPC target derivation now happens from explicit config unless `grpcTarget` is provided.

### Default API Key Removal

No SDK package now falls back to `agent-api-key-1`. Env factories return `""` when `MEMORY_SERVICE_API_KEY` is unset.

## Testing

The implementation was verified with targeted module checks:

- Python source compilation for the changed SDK packages and example apps
- TypeScript package build for `typescript/vercelai`
- Astro site build for the updated docs and snippet matches

Dedicated package unit-test suites were not added as part of this change.

## Tasks

- [x] Refactor Python LangChain transport/config helpers to centralize env resolution
- [x] Add explicit constructors and `from_env()` to LangChain SDK classes
- [x] Add explicit constructors and `from_env()` to LangGraph store classes
- [x] Introduce TypeScript `MemoryServiceConfig` and `memoryServiceConfigFromEnv()`
- [x] Update TypeScript helpers to require explicit config
- [x] Remove the hardcoded `agent-api-key-1` fallback from the SDK packages
- [x] Update Python and TypeScript example apps to use the env factories
- [x] Align existing client-configuration docs and tutorial text with the implemented API

## Files to Modify

| File | Change |
|---|---|
| `python/langchain/memory_service_langchain/transport.py` | Centralize env resolution and make transport helpers pure |
| `python/langchain/memory_service_langchain/checkpoint_saver.py` | Explicit constructor + `from_env()` |
| `python/langchain/memory_service_langchain/history_middleware.py` | Explicit constructor + `from_env()` |
| `python/langchain/memory_service_langchain/response_recording_manager.py` | Explicit constructor + `from_env()` |
| `python/langchain/memory_service_langchain/proxy.py` | Explicit constructor + `from_env()` + UDS passthrough |
| `python/langchain/memory_service_langchain/request_context.py` | Remove default API-key fallback and support explicit UDS config |
| `python/langgraph/memory_service_langgraph/transport.py` | Centralize env resolution and make transport helpers pure |
| `python/langgraph/memory_service_langgraph/store.py` | Explicit constructor + `from_env()` |
| `python/langgraph/memory_service_langgraph/async_store.py` | Explicit constructor + `from_env()` |
| `typescript/vercelai/src/index.ts` | Add explicit config type/env factory and update REST/gRPC helpers |
| `python/examples/...` | Switch examples to `from_env()` |
| `typescript/examples/...` | Switch examples to `memoryServiceConfigFromEnv()` |
| `site/src/pages/docs/python-langchain/*.mdx` | Align tutorial text with `from_env()` usage |
| `site/src/pages/docs/python-langgraph/*.mdx` | Align tutorial text with `from_env()` usage |
| `site/src/pages/docs/typescript-vecelai/*.mdx` | Align snippet matches and explicit config wording |

## Verification

```bash
find python/langchain/memory_service_langchain python/langgraph/memory_service_langgraph python/examples/langchain python/examples/langgraph \
  \( -name .venv -o -name __pycache__ \) -prune -o -name '*.py' -print0 | xargs -0 python3 -m py_compile

cd typescript/vercelai && npm run build

cd site && npm run build
```

## Design Decisions

**Why keep env factories instead of removing env support entirely?** App entrypoints and examples still benefit from env-based setup, but the resolution is now explicit and opt-in.

**Why keep low-level helper env support in Python request utilities?** The enhancement scope was the SDK construction path. The exported high-level client classes are explicit; low-level request helpers still allow env-based defaults for direct callers.
