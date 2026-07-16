# Managed Process Module Facts

- `memory-service-process` is JDK-only at runtime. Keep framework, gRPC, and Netty dependencies out
  of this module.
- Packaged binaries are discovered through `MemoryServiceBinaryProvider` service descriptors. Fat
  JAR builds must merge `META-INF/services` resources.
- Binary resolution defaults to packaged-only. Explicit file and `PATH` lookup are opt-in ordered
  resolvers; only an unavailable source falls through.
- The managed local process uses SQLite and a protected Unix domain socket. The database URL is a
  plain absolute filesystem path; do not prefix it with `sqlite://`.
- `disableUnixSocket()` removes both the default socket and its local-auth setting;
  `httpListener(...)` selects the main TCP listener, and `managementHttpListener(...)` selects a
  dedicated management TCP listener. Main TCP listeners require normal server authentication.
  Listener setters are last-wins because the server treats the main TCP port and Unix socket as
  mutually exclusive selections.
- Client targets and readiness probes are derived from the final child environment. If management
  is disabled on the main listener and no usable dedicated management socket or explicit port is
  configured, startup returns without a readiness wait.
- The child command is only `memory-service serve`. Local defaults and caller overrides use
  `MEMORY_SERVICE_*` environment variables; inherited service variables are isolated unless the
  builder explicitly enables them.
- Null output consumers use `ProcessBuilder.Redirect.DISCARD`. When stdout and stderr use the same
  non-null consumer instance, stderr is merged and one bridge thread handles both streams.
- Native provider modules exist only in the root `binary-jars` profile. Their staging input is
  `<memory-service.binaries.dir>/<platform>/{memory-service,memory-service.sha256}`.
- Keep each native provider class in a platform-specific Java package and give every published JAR
  a stable `Automatic-Module-Name`; the aggregate dependency must remain valid on the Java module
  path without split packages.
