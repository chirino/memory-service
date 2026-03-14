# TypeScript Module Facts

- `typescript/vercelai` is the publish-target package name: `@chirino/memory-service-vercelai`.
- Tutorial checkpoints consume the package via local npm `file:` dependency so imports stay unchanged pre/post publish.
- TypeScript tutorial checkpoints are under `typescript/examples/vecelai/doc-checkpoints/`.
- Site BDD fixture framework key for this track is `typescript-vecelai`.
- `typescript/vercelai` does not use a generated REST client; it proxies REST calls with plain `fetch` from `MEMORY_SERVICE_URL`, while response-recording gRPC uses generated proto loading with `@grpc/grpc-js` and an explicit target string.
- `MEMORY_SERVICE_UNIX_SOCKET` now drives both transports in `typescript/vercelai`: REST uses an `undici` agent with `connect.socketPath`, and gRPC derives `unix:///absolute/path.sock` unless an explicit gRPC target override is supplied.
