---
status: implemented
---

# Enhancement 113: Java Process API and Native Binary JARs

> **Status**: Implemented.

## Summary

Publish the Memory Service executable as three platform-specific Maven artifacts and add a
framework-neutral Java API that resolves, extracts, starts, probes, and stops a local service.
Native artifacts are assembled only by release workflows; ordinary and snapshot Maven builds
remain independent of native compilation.

## Motivation

Java applications currently need a separately installed executable or a separately managed
container. The release workflow already builds Linux AMD64, Linux ARM64, and macOS ARM64 binaries,
but those artifacts are not available on a Java application's classpath and Maven publishes before
the native artifact jobs complete.

Consumers need both a small platform-specific dependency and an easy all-platform dependency. The
runtime also needs safe extraction, deterministic binary selection, readiness gating, and lifecycle
management without adding framework, gRPC, or Netty dependencies.

## Design

### Artifacts

`memory-service-process` is part of the normal Java reactor. Three release-profile modules publish
the native providers:

- `memory-service-binary-linux-amd64`
- `memory-service-binary-linux-arm64`
- `memory-service-binary-macos-arm64`

Each provider JAR depends on `memory-service-process`, registers a
`MemoryServiceBinaryProvider` with `ServiceLoader`, and contains the executable plus its SHA-256.
The small `memory-service-binaries` JAR has no native payload; its POM depends on all three provider
JARs. Provider classes use platform-specific packages and all five JARs publish stable automatic
module names so the aggregate dependency is valid on the Java module path.

### Binary resolution and extraction

Callers configure an ordered list of packaged, explicit-file, and `PATH` resolvers. A missing
candidate advances to the next resolver, while a checksum, extraction, or launch failure is
terminal. Packaged-only resolution is the default.

Packaged executables are extracted into a configurable user cache keyed by platform and SHA-256.
Extraction uses an owner-only directory, a cross-process file lock, checksum verification, a
temporary sibling, an atomic move, and owner-only executable permissions.

### Managed local process

`MemoryServiceProcess.builder(stateDirectory)` launches an SQLite-backed local service over a Unix
domain socket with local authentication. The subprocess command is only `memory-service serve`;
the local profile and all caller overrides use the server's `MEMORY_SERVICE_*` environment-variable
surface. Typed database and listener methods update the corresponding environment entries. The
builder can disable the default Unix socket, select a main HTTP listener, or configure a dedicated
management HTTP listener. TCP listeners use the server's normal API-key or OIDC authentication.

Inherited `MEMORY_SERVICE_*` variables are isolated by default while ordinary OS environment
entries are retained. Callers can opt into service-variable inheritance, remove inherited or
default entries, and apply explicit builder entries with highest precedence. The effective main
listener controls the returned client target.

The process derives its readiness endpoint from the final environment and polls `GET /ready` over
Unix sockets or HTTP(S) with JDK networking. Management on the main listener probes the main
endpoint; an explicit dedicated management socket or port probes that endpoint. When management is
not on the main listener and no usable management address was configured, startup continues without
a readiness wait. Otherwise, startup fails on early exit or timeout. The process implements
idempotent graceful/forced shutdown through `AutoCloseable` and an optional JVM shutdown hook. Null
output consumers use `ProcessBuilder.Redirect.DISCARD`. When stdout and stderr reference the same
non-null consumer, the process builder merges stderr and one bridge thread consumes both streams.

```java
try (MemoryServiceProcess service = MemoryServiceProcess.builder(stateDirectory)
        .binaryResolvers(
                MemoryServiceBinary.file(customBinary),
                MemoryServiceBinary.packaged(),
                MemoryServiceBinary.onPath("memory-service"))
        .start()) {
    String target = service.target();
}
```

### Release handoff

The release workflow prepares the version, builds and smoke-tests all native binaries in parallel,
then downloads them into a Java release job. That job generates checksum metadata, activates the
`binary-jars` Maven profile, validates the complete bundle, and publishes it. Snapshot publication
continues to publish only the normal Java modules, including `memory-service-process`.

## Security Considerations

- Cached directories and executables are owner-only.
- Existing cached files are checksum-verified before execution.
- A resolved but corrupt or unlaunchable binary never silently falls back to another version.
- The default local server binds only a protected Unix socket and deliberately uses local-socket
  identity. Callers selecting TCP must explicitly configure normal server authentication.
- Plain local SQLite encryption is explicitly enabled by the managed-process defaults and is not a
  production remote-deployment configuration.

## Testing

- Unit-test platform aliases, provider discovery, ordered resolution, checksum validation,
  concurrent extraction, cache repair, and executable permissions.
- Launch a deterministic fake subprocess over Unix sockets and HTTP to test main and dedicated
  management readiness, unavailable-readiness continuation, output bridging, early exit, timeout,
  graceful shutdown, forced shutdown, idempotent close, environment precedence, environment
  removal, output discard, and single-thread merged output.
- Verify normal Maven builds without binary inputs and release-profile failure when inputs are
  absent.
- Package all three CI native artifacts, inspect the JARs, and execute the current-platform binary
  through the public API over both Unix sockets and HTTP.
- Validate the aggregate provider set on the Java module path to prevent split-package regressions.

## Tasks

- [x] Add the framework-neutral process API and unit tests.
- [x] Add three provider JAR modules and the aggregate JAR.
- [x] Add the release-only Maven profile and Taskfile staging contract.
- [x] Reorder release publication after native builds.
- [x] Add CI binary packaging and runtime verification.
- [x] Document dependency choices and API usage.
- [x] Verify the affected Java and site builds.
- [x] Move this document to `implemented/` and record the completed behavior.

## Files to Modify

| Area | Changes |
| --- | --- |
| `java/` | Process API, provider modules, aggregate artifact, and reactor profile |
| `Taskfile.yml` | Separate ordinary/snapshot Java publication from release binary publication |
| `.github/workflows/` | Native-to-Maven artifact handoff and CI packaging verification |
| `site/` | Framework-neutral embedded Java usage guide and navigation |
| `AGENTS.md` | Updated release workflow reference |

## Non-Goals

- Windows or Intel macOS binaries.
- In-process JNI/JNA loading.
- Remote service connection clients or gRPC channel construction.
- Native artifacts in snapshot publications.

## Verification

```bash
./java/mvnw -f java/pom.xml clean install
./java/mvnw -f java/pom.xml -Pbinary-jars \
  -Dmemory-service.binaries.dir=/tmp/memory-service-binaries clean verify
task test:site
```
