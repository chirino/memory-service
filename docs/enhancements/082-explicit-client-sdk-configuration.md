---
status: proposed
---

# Enhancement 082: Explicit Client SDK Configuration

> **Status**: Proposed.

## Summary

Refactor the Python (LangChain, LangGraph) and TypeScript (Vercel AI) client packages so that constructors accept explicit configuration only, moving environment-variable resolution into opt-in `from_env()` factory methods. Add client-configuration reference pages to the site docs for each SDK.

## Motivation

Today every client class reads `os.getenv()` / `process.env` inline during construction, with hardcoded fallback values (including a default API key of `"agent-api-key-1"`). This creates several problems:

1. **Hidden coupling** — `MemoryServiceCheckpointSaver()` (no args) silently depends on env vars that don't appear in the constructor signature or type system. A developer reading the code cannot tell what the instance connects to without knowing the env var names.

2. **Testing friction** — Users writing tests for *their own* agent code must manipulate global `os.environ` / `process.env` to control the library's behavior, which is fragile and non-hermetic.

3. **Multi-instance conflicts** — If a user needs two clients pointing at different Memory Service instances, env vars cannot express that. Explicit config can.

4. **Insecure defaults** — The hardcoded fallback `"agent-api-key-1"` means a missing env var silently produces a bogus API key instead of a clear error at startup.

5. **Composability** — Libraries embedded in frameworks, serverless functions, or containers can collide with env var conventions from other libraries. The user loses control over when and how config is resolved.

Well-designed SDKs (AWS SDK, Stripe, httpx) keep constructors explicit and offer a clearly-named `from_env()` convenience factory. This enhancement applies the same pattern.

### Current env var touchpoints

| Env Var | Packages | Default |
|---|---|---|
| `MEMORY_SERVICE_URL` | All | `http://localhost:8082` |
| `MEMORY_SERVICE_UNIX_SOCKET` | All | (empty) |
| `MEMORY_SERVICE_API_KEY` | LangChain, TypeScript | `agent-api-key-1` |
| `MEMORY_SERVICE_TOKEN` | LangGraph | (empty) |
| `MEMORY_SERVICE_GRPC_TARGET` | LangChain, TypeScript | derived from URL |
| `MEMORY_SERVICE_GRPC_PORT` | LangChain, TypeScript | inferred (443/80) |
| `MEMORY_SERVICE_GRPC_TIMEOUT_SECONDS` | LangChain | `30` |
| `MEMORY_SERVICE_GRPC_REPLAY_TIMEOUT_SECONDS` | LangChain | `None` |
| `MEMORY_SERVICE_GRPC_MAX_REDIRECTS` | LangChain | `3` |

## Non-Goals

- Changing the *names* of environment variables — the env var names stay the same.
- Changing authentication flows (Bearer token forwarding, Keycloak integration).
- Refactoring Java (Spring/Quarkus) clients — those already use typed config properties (`application.properties` / `application.yml`).

## Design

### Principle

> Constructors accept explicit values. A separate `from_env()` class method / factory function reads env vars and passes them to the constructor. Examples use `from_env()` for brevity.

### Python LangChain

#### Before

```python
class MemoryServiceCheckpointSaver:
    def __init__(self, *, base_url=None, unix_socket=None, api_key=None, ...):
        self.api_key = api_key or os.getenv("MEMORY_SERVICE_API_KEY", "agent-api-key-1")
        self._base_url = resolve_rest_base_url(base_url, unix_socket)  # reads env
        ...
```

#### After

```python
class MemoryServiceCheckpointSaver:
    def __init__(self, *, base_url: str, api_key: str, unix_socket: str | None = None, ...):
        # No env var reads — all values are explicit
        self.api_key = api_key
        self._base_url = base_url if not unix_socket else "http://localhost"
        ...

    @classmethod
    def from_env(cls, **overrides) -> "MemoryServiceCheckpointSaver":
        """Create an instance using MEMORY_SERVICE_* environment variables."""
        defaults = resolve_env_config()
        defaults.update(overrides)
        return cls(**defaults)
```

The same pattern applies to:
- `MemoryServiceHistoryMiddleware`
- `MemoryServiceResponseRecordingManager`
- `MemoryServiceProxy`

#### Shared env resolution helper

```python
# transport.py
def resolve_env_config() -> dict:
    """Read all MEMORY_SERVICE_* env vars and return a config dict."""
    unix_socket = _resolve_unix_socket_from_env()
    return {
        "base_url": _resolve_rest_base_url_from_env(unix_socket),
        "unix_socket": unix_socket,
        "api_key": os.getenv("MEMORY_SERVICE_API_KEY", ""),
    }
```

### Python LangGraph

Same pattern for `MemoryServiceStore` and `AsyncMemoryServiceStore`:

```python
class MemoryServiceStore(BaseStore):
    def __init__(self, *, base_url: str, token: str = "", unix_socket: str | None = None, ...):
        ...

    @classmethod
    def from_env(cls, **overrides) -> "MemoryServiceStore":
        defaults = resolve_env_config()
        # LangGraph uses MEMORY_SERVICE_TOKEN instead of API_KEY
        defaults["token"] = os.environ.get("MEMORY_SERVICE_TOKEN", "")
        defaults.update(overrides)
        return cls(**defaults)
```

### TypeScript / Vercel AI

#### Before

```typescript
type MemoryServiceProxyOptions = {
  baseUrl?: string;
  unixSocket?: string;
  apiKey?: string;
  authorization?: string | null;
};
// Every field optional — env vars fill the gaps at call time
```

#### After

```typescript
// Explicit config — all connection fields required
type MemoryServiceConfig = {
  baseUrl: string;
  apiKey: string;
  unixSocket?: string;
  authorization?: string | null;
};

// Env-based factory
function memoryServiceConfigFromEnv(
  overrides?: Partial<MemoryServiceConfig>,
): MemoryServiceConfig {
  const unixSocket = resolveMemoryServiceUnixSocket(overrides?.unixSocket);
  return {
    baseUrl:
      overrides?.baseUrl ??
      (unixSocket
        ? "http://localhost"
        : (process.env.MEMORY_SERVICE_URL ?? "http://localhost:8082").replace(/\/$/, "")),
    apiKey: overrides?.apiKey ?? process.env.MEMORY_SERVICE_API_KEY ?? "",
    unixSocket,
    ...overrides,
  };
}
```

Functions like `createMemoryServiceProxy()`, `withMemoryService()`, etc. accept `MemoryServiceConfig` instead of the current all-optional `MemoryServiceProxyOptions`.

### Default API key removal

All packages currently fall back to `"agent-api-key-1"`. After this change:

- `from_env()` / `memoryServiceConfigFromEnv()` returns `""` (empty string) when the env var is unset.
- Constructors accept whatever value is passed — no silent fallback.
- Example apps set the env var explicitly in their `.env` or startup scripts.

## Testing

### Unit tests

Each package should add tests that verify:

```gherkin
Scenario: Constructor requires explicit config
  Given no MEMORY_SERVICE_* environment variables are set
  When I create a MemoryServiceCheckpointSaver with base_url="http://ms:8082" and api_key="test-key"
  Then the instance should use base_url "http://ms:8082"
  And the instance should use api_key "test-key"

Scenario: from_env reads environment variables
  Given MEMORY_SERVICE_URL is set to "http://custom:9090"
  And MEMORY_SERVICE_API_KEY is set to "my-key"
  When I call MemoryServiceCheckpointSaver.from_env()
  Then the instance should use base_url "http://custom:9090"
  And the instance should use api_key "my-key"

Scenario: from_env overrides take precedence
  Given MEMORY_SERVICE_URL is set to "http://env:8082"
  When I call MemoryServiceCheckpointSaver.from_env(base_url="http://override:8082")
  Then the instance should use base_url "http://override:8082"
```

### Integration / docs tests

Existing doc-checkpoint apps switch to `from_env()` calls. Since they already rely on env vars at runtime, behavior is unchanged — tests continue to pass.

## Tasks

- [ ] Python LangChain: refactor `transport.py` — extract `resolve_env_config()`; remove env reads from `resolve_rest_base_url()`, `resolve_unix_socket()`, `resolve_grpc_target()`
- [ ] Python LangChain: refactor `MemoryServiceCheckpointSaver` — explicit constructor + `from_env()`
- [ ] Python LangChain: refactor `MemoryServiceHistoryMiddleware` — explicit constructor + `from_env()`
- [ ] Python LangChain: refactor `MemoryServiceResponseRecordingManager` — explicit constructor + `from_env()`
- [ ] Python LangChain: refactor `MemoryServiceProxy` — explicit constructor + `from_env()`
- [ ] Python LangGraph: refactor `transport.py` — same as LangChain transport
- [ ] Python LangGraph: refactor `MemoryServiceStore` + `AsyncMemoryServiceStore` — explicit constructor + `from_env()`
- [ ] TypeScript: introduce `MemoryServiceConfig` type and `memoryServiceConfigFromEnv()` factory
- [ ] TypeScript: update `createMemoryServiceProxy()`, `withMemoryService()`, and related functions to accept `MemoryServiceConfig`
- [ ] All packages: remove hardcoded `"agent-api-key-1"` default
- [ ] Update doc-checkpoint example apps to use `from_env()`
- [ ] Add unit tests for explicit constructor and `from_env()` in each package
- [ ] Site docs: add client configuration page for Python LangChain
- [ ] Site docs: add client configuration page for Python LangGraph
- [ ] Site docs: add client configuration page for TypeScript Vercel AI
- [ ] Site docs: add links to config pages from each SDK index page

## Files to Modify

| File | Change |
|---|---|
| `python/langchain/memory_service_langchain/transport.py` | Extract `resolve_env_config()`; make `resolve_rest_base_url()`, `resolve_unix_socket()`, `resolve_grpc_target()` pure (no env reads) |
| `python/langchain/memory_service_langchain/checkpoint_saver.py` | Explicit constructor + `from_env()` classmethod |
| `python/langchain/memory_service_langchain/history_middleware.py` | Explicit constructor + `from_env()` classmethod |
| `python/langchain/memory_service_langchain/response_recording_manager.py` | Explicit constructor + `from_env()` classmethod |
| `python/langchain/memory_service_langchain/proxy.py` | Explicit constructor + `from_env()` classmethod |
| `python/langgraph/memory_service_langgraph/transport.py` | Same as LangChain transport refactor |
| `python/langgraph/memory_service_langgraph/store.py` | Explicit constructor + `from_env()` classmethod |
| `python/langgraph/memory_service_langgraph/async_store.py` | Explicit constructor + `from_env()` classmethod |
| `typescript/vercelai/src/index.ts` | New `MemoryServiceConfig` type, `memoryServiceConfigFromEnv()`, update all exported functions |
| `python/examples/langchain/doc-checkpoints/*/app.py` | Switch to `from_env()` |
| `python/examples/langgraph/doc-checkpoints/*/app.py` | Switch to `from_env()` |
| `typescript/examples/vecelai/doc-checkpoints/*/src/app.ts` | Switch to `memoryServiceConfigFromEnv()` |
| `site/src/pages/docs/python-langchain/client-configuration.mdx` | New page — env var reference and explicit config examples |
| `site/src/pages/docs/python-langchain/index.mdx` | Add link to client configuration page |
| `site/src/pages/docs/python-langgraph/client-configuration.mdx` | New page — env var reference and explicit config examples |
| `site/src/pages/docs/python-langgraph/index.mdx` | Add link to client configuration page |
| `site/src/pages/docs/typescript-vecelai/client-configuration.mdx` | New page — env var reference and explicit config examples |
| `site/src/pages/docs/typescript-vecelai/index.mdx` | Add link to client configuration page |

## Verification

```bash
# Python LangChain
cd python/langchain && python -m pytest

# Python LangGraph
cd python/langgraph && python -m pytest

# TypeScript
cd typescript/vercelai && npm test

# Site build
cd site && npm run build

# Go BDD (unchanged — server-side)
go test ./internal/bdd -run TestFeaturesPgKeycloak -count=1
```

## Design Decisions

**Why `from_env()` instead of keeping the current dual pattern?** The current `param or os.getenv()` pattern couples every constructor to the process environment. `from_env()` makes that coupling visible and opt-in. Users who need explicit config (tests, multi-instance, DI frameworks) get a clean constructor. Users who want env-var convenience get a one-liner factory.

**Why not a shared config object across all classes?** Each class has different config needs (e.g., `MemoryServiceResponseRecordingManager` has gRPC-specific settings). A single shared config type would either be too broad or require splitting anyway. Each `from_env()` reads only the env vars relevant to its class.

**Why remove the default API key?** `"agent-api-key-1"` is a dev/test value that should never reach production. Falling back silently to a bogus key makes misconfiguration invisible. An empty string or explicit error is safer.
