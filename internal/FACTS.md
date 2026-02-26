# Internal Module Facts

**Error observability**: All 500-level errors MUST produce a full stack trace in the server logs. Never swallow exceptions silently — always log the stack trace for server errors.
