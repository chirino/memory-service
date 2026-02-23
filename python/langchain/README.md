# memory-service-langchain

LangChain-focused integration package for Memory Service.

Provides:
- `MemoryServiceCheckpointSaver` (LangGraph/LangChain checkpointer integration)
- `MemoryServiceHistoryMiddleware` (history capture middleware)
- `MemoryServiceResponseResumer` (checkpoint helper for resume-check/resume/cancel endpoints)
- `install_fastapi_authorization_middleware` (binds bearer token from incoming HTTP requests)
- `memory_service_scope` (binds conversation/fork context for middleware/checkpointer access)
- `memory_service_request` (authenticated async helper for proxying Memory Service REST calls)

## Local development install

```bash
uv pip install -e ./python/langchain
```

## Planned publish/install flow

```bash
uv build
uv publish

# consumer app
uv add memory-service-langchain
```
