# Spring Module Facts

**Memory repository limit gotcha**: `listConversationEntries` limit must be `<=200` (contract max). Using `1000` causes upstream `400` errors during chat memory reads and surfaces as app `500`s.

**Ownership transfer pagination parity**: `chat-spring` now forwards optional `afterCursor` and `limit` query params on `GET /v1/ownership-transfers` to `MemoryServiceProxy.listPendingTransfers(...)`.
