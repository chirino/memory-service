## TypeScript Vercel AI Facts

- SDK configuration is explicit: `MemoryServiceConfig` is passed to `createMemoryServiceProxy`, `withMemoryService`, `withProxy`, and the gRPC resume/replay/cancel helpers. Only `memoryServiceConfigFromEnv()` reads `MEMORY_SERVICE_*` environment variables.
- `memoryServiceConfigFromEnv()` returns `apiKey: ""` when `MEMORY_SERVICE_API_KEY` is unset; the package no longer falls back to `agent-api-key-1`.
