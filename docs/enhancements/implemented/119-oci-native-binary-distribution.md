---
status: implemented
---

# Enhancement 119: Distribute Native Binaries Through GHCR Instead of Maven Central

> **Status**: Implemented.

## Summary

Stop embedding the native Memory Service executables in Maven artifacts. Publish them once per
release as a public OCI artifact on GitHub Container Registry, and have `memory-service-process`
carry each platform's artifact coordinates and digests, then download, verify, and cache the
executable on first use. The `memory-service-binary-*` and `memory-service-binaries` artifact IDs
become relocation POMs that resolve to `memory-service-process`, so existing dependency
declarations keep working. The `MemoryServiceProcess` API is unchanged, and the provider SPI only
gains a default method. Applications configure registry mirrors and credentials in code, for
air-gapped networks and for registries that throttle anonymous pulls.

## Motivation

[Enhancement 113](113-java-process-and-binary-jars.md) packages the release
executables into three provider JARs. Each JAR holds a full native binary (the v0.5.0 release ZIPs
were 53.5 MB, 48.5 MB, and 58.8 MB), so one release uploads roughly 160 MB to Maven Central.

Maven Central now enforces monthly per-organization publishing limits. The
[published thresholds](https://central.sonatype.org/publish/maven-central-publishing-limits/)
put the 90th percentile at 78 MB of release size per month, and the
[Usage Center](https://central.sonatype.com/publishing/usage) holds the exact limit. The v0.6.0
release failed with:

```text
Publishing blocked: your organization has reached its monthly release size limit.
Grace period ended 2026-11-18. Usage resets 2026-12-01.
```

A single release is about twice the threshold, so a request for a higher limit at most buys time.
The Java libraries themselves are small. Only the native payload breaks the limit.

Moving the whole Java release to another Maven repository would force every consumer to add a
`<repository>` entry. GitHub Packages' Maven registry also requires a token even for public
packages. GHCR, which the release already uses for container images, serves public packages to
anonymous clients over the standard OCI distribution API. That API also works with corporate
registry mirrors and with offline copy tools such as `oras copy` and `skopeo`.

## Design

### Release artifact

The release pushes one OCI artifact per version:

```text
ghcr.io/chirino/memory-service-binary:<version>
```

It is a single image manifest with one layer per platform. Each layer is the executable
compressed with `gzip -n -9`, which is deterministic and keeps downloads near the size of today's
release ZIPs.

| Field | Value |
| --- | --- |
| Manifest `artifactType` | `application/vnd.io.github.chirino.memory-service.binaries.v1` |
| Layer `mediaType` | `application/vnd.io.github.chirino.memory-service.binary.v1+gzip` |
| Layer annotation `org.opencontainers.image.title` | `memory-service-<platform>.gz` |
| Manifest annotation `org.opencontainers.image.source` | `https://github.com/chirino/memory-service` |
| Manifest annotation `org.opencontainers.image.version` | release version |

The `source` annotation links the package to the repository. Linking grants the repository's
workflows access but does not make the package public: GitHub creates new packages as private, so
the owner must set the `memory-service-binary` package to public once, before the first release
(see [Rollout](#rollout)).

Release tags are immutable. Published `memory-service-process` JARs pin layer digests forever, so the release
never overwrites an existing version tag and package versions are never deleted. One manifest per
release keeps every layer referenced by a tag, so the registry never garbage-collects them. It
also lets mirror operators copy a whole release with one command:

```bash
oras copy ghcr.io/chirino/memory-service-binary:0.7.0 mirror.example.com/memory-service-binary:0.7.0
```

The existing container images are not reused. The `memory-service` image binary is linked against
UBI's `sqlite-libs` and `libstdc++`, while the JARs ship the static build from
`Dockerfile.portable`, and there is no macOS image.

### Bundled descriptors

`memory-service-process` carries, for each released platform, an OCI descriptor and the
executable's SHA-256 under `META-INF/memory-service/<platform>/`:

```properties
# META-INF/memory-service/linux-amd64/memory-service.oci.properties
repository=ghcr.io/chirino/memory-service-binary
reference=0.7.0
layerDigest=sha256:3f0c...
layerSize=53412345
binarySize=121634816
```

`layerDigest` and `layerSize` describe the gzip blob. `binarySize` is the uncompressed executable's
size, and `memory-service.sha256` is its digest. The client rejects a descriptor with a missing or
malformed field. All six files add a few KB to the JAR.

The module packages them from `memory-service.binaries.dir` (default `dist/java-binaries`), and
the resource includes only those files, so an executable left in the staging directory is never
packaged. Normal builds have no staged descriptors, so snapshot and developer builds package none,
and the packaged resolver then finds nothing for the platform. The `binary-descriptors` Maven
profile, which release builds and CI use, fails the build in `initialize` unless all six files are
present.

### Relocated artifacts

The release keeps publishing `memory-service-binary-linux-amd64`, `memory-service-binary-linux-arm64`,
`memory-service-binary-macos-arm64`, and `memory-service-binaries`, but only as POM-only relocation
stubs (the `binary-descriptors` profile adds those modules):

```xml
<packaging>pom</packaging>
<distributionManagement>
  <relocation>
    <artifactId>memory-service-process</artifactId>
    <message>memory-service-binaries is replaced by memory-service-process, ...</message>
  </relocation>
</distributionManagement>
```

Maven and Gradle resolve a dependency on any of them to `memory-service-process` at the same
version and print the message. The provider classes and their `META-INF/services` registrations are
gone. Applications never referenced them directly, because discovery went through `ServiceLoader`.
The stubs carry the `BACKWARD COMPATIBILITY` marker and can stop being published in a future
breaking release.

### Provider SPI

The packaged resolver first looks for a `MemoryServiceBinaryProvider` on the classpath that matches
the current platform. If there is none, it uses the descriptor bundled in `memory-service-process`,
through an internal provider. A classpath provider wins so applications can ship their own
executable, for example a repackaged JAR for offline use. Because it can also be a stale provider
JAR from an earlier release, the resolver logs at `INFO` which provider it picked over the bundled
descriptor.

Providers can't do the download themselves, because `openBinary()` takes no arguments and so can't
see the caller's mirror settings. Instead, the SPI gains a default method that exposes a
descriptor, and `memory-service-process` does the fetching:

```java
public interface MemoryServiceBinaryProvider {
    MemoryServicePlatform platform();

    InputStream openSha256() throws IOException;

    /** Embedded executable. Not called when {@link #openOciDescriptor()} returns a stream. */
    default InputStream openBinary() throws IOException {
        throw new IOException("No embedded executable for " + platform().id());
    }

    /** OCI descriptor for a downloadable executable, or null when the binary is embedded. */
    default InputStream openOciDescriptor() throws IOException {
        return null;
    }
}
```

Providers that embed an executable, such as the test provider, a consumer's own repackaged JAR, or
a provider JAR from an earlier release, keep working unchanged.

### Runtime download

When a matching provider returns a descriptor, `MemoryServiceBinary` downloads the layer with a
new internal `OciBlobFetcher`. It uses only `java.net.http` and adds no dependencies. For each
candidate location (see [Mirror rules](#mirror-rules)):

1. `GET <scheme>://<host>/v2/<name>/blobs/<layerDigest>`.
2. On `401` with `WWW-Authenticate: Bearer realm=...,service=...,scope=...`, request a token from
   the realm, anonymously or with the host's credentials as Basic auth, and retry with
   `Authorization: Bearer <token>`. On `401` with a `Basic` challenge, retry with the credentials
   directly. The realm may be on another host, as with Docker Hub. It is trusted because the
   registry named it over TLS, which is how container clients behave, and it must be HTTPS when
   credentials are sent.
3. On `404` from a mirror, `GET /v2/<name>/manifests/<reference>` once with
   `Accept: application/vnd.oci.image.manifest.v1+json`, discard the body, then retry the blob.
   Pull-through caches such as Harbor proxy projects and Artifactory or Nexus remote repositories
   often populate only when a manifest is pulled.
4. Follow redirects manually, up to five. GHCR redirects blob downloads to
   `pkg-containers.githubusercontent.com`. Send the `Authorization` header only to the location's
   host, and refuse a redirect from HTTPS to HTTP.
5. Stream the body through a `DigestInputStream` and `GZIPInputStream` into the extraction
   temporary file. Stop with an error as soon as the compressed bytes exceed `layerSize` or the
   decompressed bytes exceed `binarySize`, so a hostile or broken registry can't fill the disk.
   Fail on a size or digest mismatch at end of stream.

Each location gets up to three attempts for a connection failure, a `5xx`, or a `429`, with a
short backoff that honors `Retry-After`. Any other failure, including a digest mismatch, moves on
to the next location. The packaged resolver fails, without falling back to another resolver, once
every location has failed, and the error lists each location and why it failed.

Two time limits apply to each location: a 30-second connect timeout, and `downloadTimeout`, a
deadline for the whole attempt including the body and retries. The download runs during binary
resolution inside `start()`, before the process launches, so `startupTimeout` does not include it.

The fetcher logs through `java.util.logging`, like `MemoryServiceProcess`: one `INFO` line when a
download starts, naming the location and size, one when it completes, and a `WARNING` for each
failed location or rejected implicit token.

Apart from where the bytes come from, the existing `MemoryServiceBinary.extractProvider` flow does
not change. It already:

- checks the digest-keyed cache before it opens the binary, so a cached executable never touches
  the network
- opens the binary while holding the JVM and cross-process extraction locks, so concurrent starts
  download once
- verifies the uncompressed SHA-256 before the atomic move into the cache

Fetching the blob by digest, rather than resolving the tag, means the content address comes from
the signed Maven Central JAR and not from a mutable tag. It also skips manifest parsing and
platform selection entirely.

### Registry configuration

All download settings live in one immutable `MemoryServiceBinaryRegistries` value: mirror rules,
per-host credentials, extra trusted CAs, the proxy, and timeouts. It is configured only in code.
The library reads no system property or environment variable that can add a mirror, change where
credentials go, or trust a CA, so the binary an application runs and the registries it contacts
are decided by the application, not by whatever happens to be in the environment.

```java
// Equivalent to MemoryServiceBinary.packaged(): upstream ghcr.io, GH_TOKEN/GITHUB_TOKEN pickup on.
MemoryServiceBinaryRegistries defaults = MemoryServiceBinaryRegistries.defaults();
```

| Builder method | Purpose | Default |
| --- | --- | --- |
| `mirror(String prefix, String location)` | Add a mirror rule. Repeat to add more, tried in order | none |
| `upstreamFallback(boolean)` | Try the upstream registry after every mirror fails | `false` when mirrors are set |
| `credentials(String host, String username, String secret)` | Credentials for one registry host. Repeat for more hosts | none |
| `githubTokenFromEnvironment(boolean)` | Use `GH_TOKEN`/`GITHUB_TOKEN` for `ghcr.io` | `true` |
| `trustedCertificates(Path)` | Trust extra CA certificates from a PEM file | JVM trust store only |
| `proxy(ProxySelector)` | Proxy selection for registry requests | `ProxySelector.getDefault()` |
| `downloadTimeout(Duration)` | Deadline for one location, including the body and retries | 5 minutes |

`MemoryServiceBinary.packaged()` keeps its signature and means
`packaged(MemoryServiceBinaryRegistries.defaults())`. To use other settings, pass them through
the existing resolver list:

```java
MemoryServiceProcess.builder(stateDirectory)
        .binaryResolvers(MemoryServiceBinary.packaged(registries))
        .start();
```

`build()` validates the configuration: malformed mirror rules, a blank credential host, a missing
CA file, and credentials for a host reached only over `http://` all fail there, before any
download starts.

#### Mirror rules

Mirror rules follow the `prefix` and `location` model from containers' `registries.conf`. A rule
maps an upstream repository prefix to a mirror location. The longest matching prefix wins, and the
rest of the repository path is appended to the location:

| Rule | Upstream `ghcr.io/chirino/memory-service-binary` resolves to |
| --- | --- |
| `mirror("ghcr.io", "harbor.corp.example/ghcr-proxy")` | `harbor.corp.example/ghcr-proxy/chirino/memory-service-binary` |
| `mirror("ghcr.io/chirino/memory-service-binary", "nexus.corp.example:8443/tools/memory-service-binary")` | `nexus.corp.example:8443/tools/memory-service-binary` |
| `mirror("ghcr.io", "http://zot.lab.internal:5000")` | `http://zot.lab.internal:5000/chirino/memory-service-binary` |

Several rules for the same prefix are tried in the order they were added. Locations use HTTPS
unless they start with `http://`. A repository that no rule matches goes to the upstream registry.
`mirror(...)` rejects a prefix with a scheme, a malformed host or path, and a location without a
repository path when the prefix is more than a registry host, because the mirror would have no
repository to append to.

Upstream fallback is off by default once mirrors are configured. In an air-gapped network, an
attempt to reach `ghcr.io` can only fail, often slowly, and an unexpected outbound connection
attempt may trip egress alerts.

#### Registry credentials

Credentials always belong to a registry host and are used only for that host and the token realm
it names, so a GitHub token meant for `ghcr.io` never reaches a mirror, and a mirror's robot token
never reaches GitHub. For
each location, the fetcher uses the first match:

1. Credentials added with `credentials(host, username, secret)`, matched on `host:port` and then
   on the bare host.
2. For `ghcr.io` only, unless `githubTokenFromEnvironment(false)` was set: the `GH_TOKEN`
   environment variable, then `GITHUB_TOKEN`. That is the precedence the `gh` CLI uses, and many
   CI setups and developer shells already export one of them. The token is never sent to any
   other host, including mirrors.
3. Otherwise, the anonymous token flow.

The GitHub token lookup is the only environment variable the library reads for downloads. It can
only add credentials for the upstream host, never redirect a download, and the token is sent only
when the token realm that `ghcr.io` names is itself on `ghcr.io`. The artifact is public, so the
token is never required. It only helps when GitHub throttles anonymous pulls. A token picked up implicitly can be expired
or lack `read:packages`, and GHCR would then reject the token exchange. In that case the fetcher
logs a warning that names the variable and retries anonymously, so an unrelated token in the
environment never breaks a download. Credentials added with `credentials(...)` were configured on
purpose, so their rejection is reported as an error for that location.

Credentials are used as Basic auth against the registry's token realm in the standard token
exchange, or directly when the registry sends a `Basic` challenge. Registries that accept a bare
token, such as GHCR, take any username, so `credentials("ghcr.io", "token", githubToken)` works.

Credentials are never sent to an `http://` location. A plain-HTTP mirror must allow anonymous
pulls. Content integrity doesn't depend on transport, because both digests come from the signed
JAR, but credentials would.

The trusted CA file adds certificates to the JVM's default trust store rather than replacing it, so
public upstream TLS keeps working when fallback is enabled.

#### Examples

A GitHub Actions job gets authenticated `ghcr.io` pulls with the defaults by exposing the workflow
token the usual way. Actions does not export `GITHUB_TOKEN` to steps by default:

```yaml
env:
  GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
```

An application that must never send an ambient GitHub token anywhere:

```java
MemoryServiceBinary.packaged(MemoryServiceBinaryRegistries.builder()
        .githubTokenFromEnvironment(false)
        .build());
```

An air-gapped deployment that pulls through a private Harbor proxy with an internal CA. The
application decides where the robot token comes from:

```java
MemoryServiceBinaryRegistries registries = MemoryServiceBinaryRegistries.builder()
        .mirror("ghcr.io", "harbor.corp.example/ghcr-proxy")
        .credentials("harbor.corp.example", "robot$memory-service", config.harborToken())
        .trustedCertificates(Path.of("/etc/pki/tls/certs/corp-ca.pem"))
        .githubTokenFromEnvironment(false)
        .build();

try (MemoryServiceProcess service = MemoryServiceProcess.builder(stateDirectory)
        .binaryResolvers(MemoryServiceBinary.packaged(registries))
        .start()) {
    // ...
}
```

A mirror first, then upstream with credentials from the application's own secret store:

```java
MemoryServiceBinaryRegistries registries = MemoryServiceBinaryRegistries.builder()
        .mirror("ghcr.io", "artifactory.corp.example/ghcr-remote")
        .upstreamFallback(true)
        .credentials("artifactory.corp.example", "ci", secrets.get("artifactory-token"))
        .credentials("ghcr.io", "token", secrets.get("ghcr-token"))
        .build();
```

The library does not read Docker or containers auth files. Applications that want to reuse a
`docker login` or `podman login` can parse those files and call `credentials(...)` themselves.

Frameworks that want externalized configuration, such as a Quarkus or Spring Boot integration,
map their own configuration properties onto this builder. That keeps the configuration under the
application's normal configuration model.

This enhancement doesn't change what the child process inherits. Like other ordinary OS
variables, `GH_TOKEN` and `GITHUB_TOKEN` are still inherited unless the caller removes them with
the existing environment-removal builder options. The server does not read them.

### Populating a mirror

A pull-through cache needs no setup beyond a project or remote repository pointed at `ghcr.io`.
For a disconnected registry, an operator copies each release across the air gap with standard
tools:

```bash
# Connected side
oras copy --to-oci-layout ghcr.io/chirino/memory-service-binary:0.7.0 ./memory-service-binary:0.7.0
# Disconnected side
oras copy --from-oci-layout ./memory-service-binary:0.7.0 \
  harbor.corp.example/ghcr-proxy/chirino/memory-service-binary:0.7.0
```

`skopeo copy` works the same way. The embedded Java guide documents both.

### Proxies and offline use

The HTTP client uses `ProxySelector.getDefault()` unless the application sets `proxy(...)`, so the
JVM's standard `https.proxyHost`, `https.proxyPort`, and `http.nonProxyHosts` system properties
apply (the JDK uses `http.nonProxyHosts` for HTTPS too). The library
does not parse `HTTPS_PROXY` or `NO_PROXY`. A proxy can't change the content, because both digests
are verified, and can't read credentials, because they only travel inside TLS.

Hosts without any registry access keep the existing `MemoryServiceBinary.file(...)` and
`onPath(...)` resolvers. A build step can also pre-populate the cache on a connected machine with
the existing public API and ship the directory, for example in a container image layer:

```java
MemoryServiceBinary.resolve(List.of(MemoryServiceBinary.packaged(registries)), cacheDirectory);
```

The cache layout stays `<cacheDirectory>/<platform>/<sha256>/memory-service`.

### Release workflow

```text
binary-artifact (x3) ──► binary-oci ──► java-release ──► github-release
```

Both jobs use `.github/scripts/binary-oci.sh`, which CI also runs against a local registry:
`push` gzips and pushes the three executables, and `stage` pulls the manifest and layers, checks
each layer's digest and size, and writes each platform's executable, `memory-service.sha256`, and
`memory-service.oci.properties`. With `ORAS_ANONYMOUS=true`, `stage` ignores stored registry
credentials.

- **`binary-oci`** (new job, `packages: write`): if the version tag already exists, do nothing.
  A rerun rebuilds the binaries, and the rebuilt bytes are not guaranteed to match, so the
  registry stays the source of truth and the tag is never overwritten. Otherwise download the
  three `release-binary-*` artifacts, gzip each one, and push the artifact with `oras push` using
  `GITHUB_TOKEN`. Only a registry "not found" answer counts as a missing tag; any other error
  fails the job rather than risk an overwrite.
- **Java release job** (`needs: binary-oci`): pull the manifest and all three layers from GHCR
  **anonymously**, verify each layer against its manifest digest, and decompress it. Write each
  platform's `memory-service.oci.properties` and `memory-service.sha256` from those pulled bytes,
  not from the workflow artifacts. That guarantees the digests published to Maven Central match
  what clients download, including on a rerun, and it proves the package is public. The job fails
  before publishing to Maven Central if any step fails.
- **`finish`** (`needs: binary-oci`): build the GitHub Release ZIPs from an anonymous pull of the
  layers too, so the ZIPs and the OCI artifact hold the same bytes, including on a rerun where the
  tag already existed.

The GitHub Release keeps attaching the native ZIPs for direct download.

### Rollout

- Before the first release with this change, the owner creates the `memory-service-binary`
  package by pushing a throwaway tag, and sets its visibility to public. The anonymous pull in
  the Java release job enforces this on every release.
- Earlier releases are unaffected. Their provider JARs embed the executable and keep working with
  any `memory-service-process` version.
- Consumers that declare `memory-service-binaries` or a `memory-service-binary-<platform>`
  artifact get `memory-service-process` through the relocation and a build warning telling them to
  switch. An application that upgrades only `memory-service-process` and keeps an older provider
  JAR runs that older executable, and the resolver logs which provider it used.
- Maven Central refuses new releases while the organization is over its monthly size limit, even
  small ones. The 0.6.0 release is abandoned, and 0.7.0 is the first release with this change,
  published after the limit resets on 2026-12-01.
- Reverting means going back to embedded JARs, which Maven Central would reject again, so there
  is no rollback path beyond fixing forward. Published versions stay valid as long as their GHCR
  package versions exist.

### Maven Central usage after the change

The release publishes 16 Java modules and four relocation POMs. With the native payload gone,
release size drops from about 160 MB to a few MB per release. A JAR module publishes about 20
files (POM, JAR, sources, Javadoc, signatures, checksums) and a relocation POM about 5, so a release
is about 340 files. That is within the published threshold for up to three releases a month, so
release count and file count become the limits to watch.

## Design Decisions

- **GHCR over Docker Hub.** The release already authenticates to GHCR with `GITHUB_TOKEN`, so no
  new secrets or accounts are needed. Docker Hub would also work, and blob-by-digest downloads
  would avoid its manifest-based pull limits.
- **OCI over GitHub Release assets.** Release-asset URLs are simpler to fetch, but OCI blobs are
  content-addressed and work with registry mirrors, pull-through caches, and `oras copy` for
  air-gapped environments.
- **Descriptors in `memory-service-process`, old artifacts relocated.** Once the executables
  left the provider JARs, those JARs held only a few hundred bytes of descriptor each. Bundling
  all descriptors in `memory-service-process` removes three JAR modules from every release, and
  the provider classes and their version-skew shim with them, so the descriptor always matches the
  process library. Relocation POMs keep existing dependency declarations resolving. The cost is
  that every application carries all three platforms' descriptors, a few KB in total. The SPI stays
  for applications that supply their own executable.
- **Single manifest, three layers.** The runtime never resolves the tag, so a per-platform index
  adds release complexity without a client benefit.
- **Registry settings only in code.** Environment variables or system properties that add a
  mirror would let anything that can set the environment change which hosts the application
  contacts and where credentials go. Applications and framework integrations map their own
  configuration onto the builder instead.
- **Container-tool conventions for mirrors.** Mirror rules follow `registries.conf`, so the
  mapping is familiar to anyone who has configured a container mirror.
- **No credential discovery beyond the GitHub token.** Reading Docker or containers auth files
  means handling several file locations, formats, and credential helpers. Applications that want
  it can parse those files and pass the results to `credentials(...)`.
- **Implicit GitHub tokens are best-effort and can be disabled.** Picking up `GH_TOKEN` or
  `GITHUB_TOKEN` helps most CI users with no configuration. It only ever adds credentials for
  `ghcr.io`, falls back to anonymous on rejection, and `githubTokenFromEnvironment(false)` turns it
  off.
- **Gzip layers.** An uncompressed layer would make the layer digest equal the executable digest,
  but would roughly double the download size. The JDK's `GZIPInputStream` makes decompression
  free in code.

## Security Considerations

- The layer digest and the executable SHA-256 both come from the GPG-signed `memory-service-process` JAR on Maven
  Central. A tampered registry, mirror, or redirect target cannot substitute content without
  failing one of the two checks.
- Credentials and bearer tokens are scoped to one registry host. They are never sent to a mirror
  configured for a different host, never forwarded on redirect, and never sent over `http://`.
  `GH_TOKEN` and `GITHUB_TOKEN` are only ever sent to `ghcr.io`.
- `http://` is allowed only for a mirror location written with that scheme. Upstream locations,
  token realms that receive credentials, and any redirect from HTTPS must stay on HTTPS.
- Downloads are bounded by the `layerSize` and `binarySize` from the signed JAR and by
  `downloadTimeout`, so a hostile registry can't exhaust disk or stall startup indefinitely.
- GHCR version tags are never overwritten and package versions are never deleted, because
  published JARs pin their digests.
- Error messages and logs name the credential source, such as `GITHUB_TOKEN` or the host that
  `credentials(...)` was added for, and never include secret values.
- Cache and executable permissions, and the rule that a resolved but corrupt binary never falls
  back to another resolver, carry over unchanged from Enhancement 113.
- Once every registry location has failed, the packaged resolver fails like an extraction failure
  does today, so a network error never silently selects a different binary.

## Testing

Unit tests in `memory-service-process` (`OciBlobFetcherTest`, `MemoryServiceBinaryRegistriesTest`,
and `MemoryServiceBinaryTest`), using the JDK's `com.sun.net.httpserver.HttpsServer` as a fake
registry (`FakeRegistry`), with a checked-in test keystore and matching PEM certificate. A
package-private builder hook stands a fake server in for `ghcr.io`, so the GitHub token rules run
end to end. The tests cover:

- anonymous blob download without a challenge
- `401` challenge, anonymous token exchange, and retry
- a redirect to a second host that receives no `Authorization` header
- rejection of an `http://` redirect target
- layer digest mismatch, size mismatch, and a corrupt gzip stream
- a body longer than `layerSize` and a gzip stream that expands past `binarySize` are cut off
- a malformed descriptor is rejected
- retry on connection failure, `5xx`, and `429` with `Retry-After`, then move to the next location
- a stalled body fails at `downloadTimeout`
- a token realm on another host receives explicit credentials only over HTTPS, and never receives
  the implicit GitHub token
- the bundled descriptor is used when no classpath provider matches, a matching classpath
  provider takes precedence over it, and a platform with neither falls through to the next
  resolver (`MemoryServiceBinaryTest`, with descriptors served by a test class loader)
- a release build bundles a descriptor and digest for every platform and no executable
  (`PackagedMemoryServiceProcessTest`)
- an uncompressed SHA-256 mismatch rejected by the existing extraction check
- a cached executable is used without any HTTP request
- concurrent `extractProvider` calls produce exactly one blob download
- mirror rule validation, longest-prefix matching, ordering, and upstream fallback on and off
- `404` from a mirror triggers one manifest pull and a blob retry
- `defaults()` resolves only the upstream location and reads no `MEMORY_SERVICE_BINARY_*` variable
- credential selection order: explicit credentials, then the GitHub token, then anonymous
- `GH_TOKEN` takes precedence over `GITHUB_TOKEN`, both are sent only to `ghcr.io`, and a rejected
  implicit token falls back to anonymous with a warning
- `githubTokenFromEnvironment(false)` sends no token even when both variables are set
- explicit credentials are never sent to another host or to an `http://` location, and `build()`
  rejects credentials for an `http://`-only host
- a fake registry's certificate is trusted only through the custom CA file
- a configured `proxy(...)` is used for registry and redirect requests

CI (`test-binary-jars` in `.github/workflows/ci.yml`): push the CI-built binaries to a throwaway
local OCI registry (`registry:2` service container) with the same `oras push` script the release
uses. Generate descriptors that name the real upstream repository, because upstream locations must
be HTTPS. The job builds `memory-service-process` and the relocation POMs with
`-Pcentral-release,binary-descriptors`. `PackagedMemoryServiceProcessTest`, in
`memory-service-process`, configures `mirror("ghcr.io", "http://localhost:5000")` from the
`memory-service.packaged.test.mirror` system property, which also exercises the mirror path end to
end. The job runs the downloaded binary over Unix sockets and HTTP, asserts that the
`memory-service-process` JAR contains every platform's descriptor and digest, no executable, and is
smaller than 500 KB, and validates it on the module path.

Release: the Java release job's anonymous pull and digest check run before the Maven Central
publish step.

## Tasks

- [x] Add `openOciDescriptor()` to the provider SPI and teach the packaged resolver to download.
- [x] Add `OciBlobFetcher` with the challenge, token, redirect, manifest-warm, digest, gzip, size
  bound, retry, deadline, and logging behavior.
- [x] Add the code-only `MemoryServiceBinaryRegistries` builder (mirror rules, credentials, GitHub
  token pickup and its off switch, CA file, proxy, timeout) and
  `MemoryServiceBinary.packaged(registries)`.
- [x] Bundle every platform's descriptor and digest in `memory-service-process`, with a
  `binary-descriptors` profile that requires them.
- [x] Replace the three platform JARs and `memory-service-binaries` with relocation POMs.
- [x] Create the `memory-service-binary` GHCR package and set it to public. Done with the test
  tag `0.0.0-test.1` (the v0.5.0 executables), which was then verified end to end against
  `ghcr.io` on macOS ARM64, Linux ARM64, and Linux AMD64.
- [x] Add the `binary-oci` release job with the never-overwrite rule.
- [x] Stage Java release inputs from an anonymous pull of the pushed layers.
- [x] Build GitHub Release ZIPs from the pulled layers.
- [x] Update `test-binary-jars` to use a local registry and assert the bundled descriptors.
- [x] Update the embedded Java guide: first-run download, mirrors, credentials, populating a
  mirror, proxies, and offline use.
- [x] Move this document to `implemented/` and record what was built.

## Files to Modify

| File | Changes |
| --- | --- |
| `java/memory-service-process/src/main/java/io/github/chirino/memoryservice/process/MemoryServiceBinaryProvider.java` | Default `openBinary()` and new `openOciDescriptor()` |
| `java/memory-service-process/src/main/java/io/github/chirino/memoryservice/process/MemoryServiceBinary.java` | `packaged(MemoryServiceBinaryRegistries)`; bundled-descriptor fallback and download path in the packaged resolver |
| `java/memory-service-process/src/main/java/io/github/chirino/memoryservice/process/BundledBinaryProvider.java` | New: reads the descriptors bundled under `META-INF/memory-service/` |
| `java/memory-service-process/pom.xml` | Package staged descriptors; `binary-descriptors` profile validates them |
| `java/memory-service-process/src/main/java/io/github/chirino/memoryservice/process/MemoryServiceBinaryRegistries.java` | New: code-only mirror, credential, CA, and timeout settings |
| `java/memory-service-process/src/main/java/io/github/chirino/memoryservice/process/OciBlobFetcher.java` | New: OCI blob download with token exchange and digest verification |
| `java/memory-service-process/src/test/java/io/github/chirino/memoryservice/process/OciBlobFetcherTest.java` | New: fake-registry download, auth, redirect, bound, retry, and timeout tests |
| `java/memory-service-process/src/test/java/io/github/chirino/memoryservice/process/FakeRegistry.java` | New: HTTP and HTTPS fake OCI registry for tests |
| `java/memory-service-process/src/test/java/io/github/chirino/memoryservice/process/MemoryServiceBinaryTest.java` | Download, cache-hit, checksum, and concurrent-download tests for OCI providers |
| `java/memory-service-process/src/test/resources/registry-tls/` | New: test TLS keystore and PEM certificate for the fake registry |
| `java/memory-service-process/src/test/java/io/github/chirino/memoryservice/process/MemoryServiceBinaryRegistriesTest.java` | New: mirror rules, validation, and credential selection tests |
| `java/memory-service-binary-{linux-amd64,linux-arm64,macos-arm64}/pom.xml`, `java/memory-service-binaries/pom.xml` | Relocation POMs; provider classes, service files, and sources removed |
| `java/pom.xml` | `binary-jars` profile renamed `binary-descriptors` |
| `java/memory-service-process/src/test/java/io/github/chirino/memoryservice/process/PackagedMemoryServiceProcessTest.java` | Moved from `memory-service-binaries`; runs the bundled descriptor's binary through a test-configured mirror |
| `.github/scripts/binary-oci.sh` | New: `push` and `stage` for the OCI artifact, shared by release and CI |
| `.github/workflows/release.yml` | New `binary-oci` job with `packages: write`; Java release staging and `finish` ZIPs from an anonymous pull; Central checks look for the relocation POMs |
| `.github/workflows/ci.yml` | `test-binary-jars` uses a local registry, and the JAR inspection asserts descriptors, no executable, and size |
| `Taskfile.yml` | `release:java` and `release:java:bundle` expect descriptor inputs in `BINARIES_DIR` and publish the relocation POMs |
| `site/src/pages/docs/deployment/embedded-java.mdx` | Single `memory-service-process` dependency and relocation note; first-run download, mirrors, credentials, populating a mirror, proxies, and offline resolvers |
| `docs/enhancements/implemented/113-java-process-and-binary-jars.md` | Link to this enhancement for the changed artifact contents |

## Non-Goals

- Publishing the binaries anywhere other than GHCR and the GitHub Release.
- Reading Docker or containers auth files, or running credential helpers (`credsStore`,
  `credHelpers`). Applications can do this themselves and pass the results to `credentials(...)`.
- Running `gh auth token` or other external commands to discover credentials.
- Parsing `HTTPS_PROXY` or `NO_PROXY`. Applications use JVM proxy properties or `proxy(...)`.
- A dedicated Maven plugin or CLI for pre-populating the cache. The existing
  `MemoryServiceBinary.resolve(...)` API covers it.
- Signing the OCI artifact with cosign or notation. The Maven Central signature already covers
  the digests the client trusts.
- New platforms, or changes to the Docker images.

## Verification

```bash
./java/mvnw -f java/pom.xml -pl :memory-service-process -am clean verify

# End to end through a local registry. Put an executable at /tmp/native/<platform>/memory-service
# for all three platforms; only the current platform's needs to be real.
docker run -d --rm --name binary-registry -p 127.0.0.1:5055:5000 registry:2
export ORAS_PLAIN_HTTP=true
.github/scripts/binary-oci.sh push localhost:5055/chirino/memory-service-binary:dev dev /tmp/native
.github/scripts/binary-oci.sh stage localhost:5055/chirino/memory-service-binary:dev \
  ghcr.io/chirino/memory-service-binary /tmp/memory-service-binaries
./java/mvnw -f java/pom.xml -Pcentral-release,binary-descriptors \
  -Dmemory-service.binaries.dir=/tmp/memory-service-binaries \
  -Dmemory-service.packaged.test=true \
  -Dmemory-service.packaged.test.mirror=http://localhost:5055 \
  -Dgpg.skip=true -Dcentral.skipPublishing=true \
  -pl :memory-service-process,:memory-service-binary-linux-amd64,:memory-service-binary-linux-arm64,:memory-service-binary-macos-arm64,:memory-service-binaries \
  -am clean install
docker stop binary-registry
actionlint .github/workflows/ci.yml .github/workflows/release.yml
task test:site
```
