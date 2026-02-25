# Memory Service Module Facts

**Error observability**: All 500-level errors MUST produce a full stack trace in the server logs. The `GlobalExceptionMapper` in `memory-service/src/main/java/.../api/GlobalExceptionMapper.java` catches unhandled exceptions and logs them with `LOG.errorf(e, ...)`. When adding new endpoints or error paths, never swallow exceptions silently — always log the stack trace for server errors.
