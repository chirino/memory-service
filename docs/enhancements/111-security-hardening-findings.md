---
status: proposed
---

# Enhancement 111: Security Hardening Findings

> **Status**: Proposed.

## Summary

This review partially agrees with the original findings. The deployment-default,
container, CORS, OIDC-audience, error-handling, management-listener, and production
configuration concerns are supported by the current code. Two claims were removed:
Go map lookup does not create a useful API-key prefix-timing oracle, and Go toolchain
pinning is project hygiene rather than a security finding. Root containers and a
missing Kubernetes `securityContext` were also reduced from Critical to High because
they amplify a separate compromise but are not, by themselves, a container escape.

The broader code review found eight material issues not covered by the original draft:

1. attachment streams use unauthenticated AES-CTR and can be modified without detection;
2. user-controlled active attachment types can execute on the service's origin;
3. OIDC `scope` and `groups` values are treated as application roles, so `scope=admin`
   can satisfy the default administrator-role check;
4. a manual release input is interpolated into shell source before validation;
5. the Fly deploy script enables shell tracing while handling generated secrets;
6. the encryption guide uses a stale provider-selection variable and overstates stream
   encryption guarantees;
7. TLS and plaintext are enabled together by default, leaving a downgrade path unless
   deployment networking prevents direct plaintext access; and
8. both React frontends open attachment-provided URLs without validating their schemes.

The highest-priority work is authenticated attachment encryption, attachment content
isolation, OIDC claim separation, release/deploy script repair, and least-privilege
container deployment. This document is an umbrella findings and decision record, not one
monolithic change set; it is intended for the independent implementation workstreams below.
Operator-facing recommendations will be published as a dedicated site security guide rather
than requiring deployers to interpret this document.

## Motivation

The service stores conversation history, memory, and attachments for agent applications.
Some user APIs and the developer frontend are intentionally browser-accessible, while
deployment configurations integrate with identity providers, datastores, object storage,
and release credentials. A compromise can therefore expose both confidential model data
and credentials used to reach adjacent systems.

Hardening must cover more than secret defaults. It must also preserve integrity of stored
content, prevent stored content from becoming active same-origin code, keep identity claims
within their intended authorization domain, and make unsafe deployment combinations fail
before listeners start.

## Scope and Methodology

This was a static review of the current local tree. It covered:

| Domain | Reviewed surfaces |
|---|---|
| Go application | authentication/authorization, routing, attachments, encryption providers, configuration, listeners, errors, SSRF defenses, and representative store queries |
| Browser clients | attachment-link handling, HTML injection sinks, authentication storage, and developer frontend proxy/base-URL behavior |
| Deployment | production Dockerfile, Compose, Kustomize, Fly, Keycloak, monitoring, and devcontainer assets |
| CI/CD | workflow triggers, permissions, action pinning, release input handling, and secret use |
| Java | production client/extension modules and example security configuration; see F-J1 |

This review did not include dynamic penetration testing, dependency-vulnerability
resolution, or cloud-environment policy inspection.

### Revalidation Corrections

- The old F-C1/F-C2 findings are valid but are High, not Critical. Running as root and
  omitting a `securityContext` increase the impact of an RCE; neither is the initial
  container-escape primitive claimed by the draft.
- The API-key constant-time finding was removed. Go map lookup hashes the full candidate
  using a process-randomized hash; it is not an early-exit string comparison that exposes
  a useful prefix oracle for high-entropy API keys.
- Go patch/toolchain pinning was removed from this security proposal. It remains a valid
  reproducibility convention in `AGENTS.md`.
- Root-level logs and screenshots are ignored by Git, not committed. The remaining issue
  is that the sparse `.dockerignore` can still send ignored artifacts into Docker build
  context and the builder's `COPY . .`.
- `MEMORY_SERVICE_ATTACHMENT_SIGNING_SECRET` is obsolete. Attachment signing keys are
  derived from configured encryption providers; the DEK provider uses HKDF-SHA256 over
  `MEMORY_SERVICE_ENCRYPTION_DEK_KEY`.
- `MEMORY_SERVICE_MODE` is not read by the serve command. `config.DefaultConfig()` uses
  production mode and tests set testing mode programmatically, so hardening must not
  document that environment variable as an escape hatch.
- The Java production tree is primarily client libraries and framework integrations, not
  server-side resources or Panache repositories. Its follow-up audit should focus on
  credential propagation, redirects, logging, transport, and Unix-socket proxy behavior.

## Findings

Severity reflects the worst credible deployment impact. Findings marked **dev/demo** are
not production vulnerabilities unless those assets are exposed or reused outside their
intended environment.

### Critical

No independently exploitable Critical finding was confirmed in this static review.

### High

#### F-H1 — Production container runs as root

- **Location**: [Dockerfile](../../Dockerfile), runtime stage.
- **Impact**: An application RCE starts as UID 0 inside a writable container, increasing
  the effect of kernel/runtime weaknesses and writable mounted paths.
- **Fix**: Use a fixed non-root UID, make copied assets readable by that UID, and verify
  the built image's configured user in CI.

#### F-H2 — Kubernetes workload lacks a security context and mounts an unnecessary token

- **Location**: `deploy/kustomize/base/deployment.yaml` and its `ServiceAccount`.
- **Impact**: The pod permits root execution, privilege escalation, a writable root
  filesystem, default capabilities, and the default seccomp posture. Kubernetes also
  automounts a service-account token even though this repository defines no workload RBAC;
  an RCE gains a namespace identity whose future bindings may expand silently.
- **Fix**: Set `runAsNonRoot`, a fixed UID/GID, `allowPrivilegeEscalation: false`,
  `readOnlyRootFilesystem: true`, `capabilities.drop: [ALL]`, and
  `seccompProfile.type: RuntimeDefault`; set `automountServiceAccountToken: false` unless
  an explicit Kubernetes API use is added.

#### F-H3 — Compose provides a publicly known at-rest encryption DEK

- **Location**: [compose.yaml](../../compose.yaml),
  `MEMORY_SERVICE_ENCRYPTION_DEK_KEY` fallback.
- **Impact**: Reusing the Compose configuration without an override exposes stored data
  and the HKDF-derived attachment-token signing key to anyone who knows the repository.
- **Fix**: Keep the dev value in a clearly local-only env file or generate it at setup;
  never provide a usable production fallback. Reject the known value during production
  validation.

#### F-H4 — Fixed OIDC and Qdrant secrets in reusable dev configuration **(dev/demo)**

- **Location**: `deploy/keycloak/memory-service-realm.json` and [compose.yaml](../../compose.yaml).
- **Impact**: Reusing the imported realm or Qdrant configuration outside a local machine
  exposes a confidential OIDC client and the vector service. Broad dev origins and
  redirects make realm reuse more dangerous.
- **Fix**: Generate per-environment values and exact origins/redirects; make the example
  refuse non-local exposure while placeholders remain.

#### F-H5 — Grafana exposes anonymous administrator access **(dev/demo)**

- **Location**: [compose.yaml](../../compose.yaml), Grafana environment and published port.
- **Impact**: Anyone reaching the port receives administrator access without login.
- **Fix**: Disable anonymous access, generate the admin password, and bind local dev ports
  to loopback.

#### F-H6 — Demo gateway exposes Prometheus and Grafana without an auth boundary **(demo)**

- **Location**: `deploy/kustomize/components/demo/gateway/httproute-prometheus.yaml` and
  `httproute-grafana.yaml`.
- **Impact**: A reachable demo gateway exposes operational data and, with F-H5, Grafana
  administration.
- **Decision**: Remove both public monitoring `HTTPRoute` resources. Keep Prometheus,
  Grafana, and management listeners cluster-internal; this enhancement does not add an
  authentication proxy merely to preserve demo routes that have no known consumer.

#### F-H7 — Devcontainer copies host SSH keys into the container **(dev)**

- **Location**: `.devcontainer/devcontainer.json` and `.devcontainer/post-start.sh`.
- **Impact**: Repository code, dependencies, extensions, and tools can read copied private
  keys. The read-only host-home bind also exposes considerably more host data than needed.
- **Decision**: Remove the host-home bind and stop copying `.ssh` into the container. Use
  the editor/devcontainer SSH-agent integration for private-key operations and mount or copy
  only non-secret Git configuration and `known_hosts` when required.

#### F-H8 — Privileged Docker-in-Docker compounds the host-home exposure **(dev)**

- **Location**: `.devcontainer/devcontainer.json`.
- **Impact**: The privileged inner daemon substantially expands the devcontainer attack
  surface. It is not automatically root access to the host daemon, but combining it with
  broad host data mounts increases the impact of malicious workspace code.
- **Decision**: Retain isolated Docker-in-Docker because the repository's integration tests
  require Compose and nested containers. Do not mount the host Docker socket, which would
  give workspace code control of the host daemon. Pin the devcontainer feature to an exact
  released version, remove the host-home/SSH-key exposure in F-H7, and document the remaining
  privileged-container requirement as a trusted-workspace, development-only risk. Rootless
  Docker is not selected here because it still needs elevated outer-container setup and has
  networking/storage limitations that would make the current test matrix unreliable.

#### F-H9 — Unauthenticated SOCKS proxy is published from the devcontainer **(dev)**

- **Location**: `.devcontainer/supervisord.conf` and `.devcontainer/devcontainer.json`.
- **Impact**: A reachable unauthenticated proxy is an open relay and a pivot into networks
  visible from the container.
- **Decision**: Remove microsocks, its supervisor entry, and port 1080 from the devcontainer.
  No repository task or documented workflow depends on this proxy, so retaining an
  authenticated or loopback-only variant adds maintenance without a current use case.

#### F-H10 — Production and operational images use mutable tags

- **Location**: [Dockerfile](../../Dockerfile), [compose.yaml](../../compose.yaml), and
  Kustomize manifests using `latest` or otherwise floating tags.
- **Impact**: Upstream changes land without review, rollback becomes ambiguous, and a tag
  compromise propagates silently.
- **Fix**: Pin third-party images by digest and first-party deployment images to immutable
  release identifiers/digests; automate deliberate refreshes.

#### F-H11 — Production can silently use plaintext encryption and fail-open fallback

- **Location**: `internal/config/config.go` defaults encryption providers to `plain`;
  `internal/dataencryption/service.go` routes headerless or malformed-envelope data to
  `plain` whenever it is registered.
- **Impact**: A healthy production process may store cleartext. During mixed-provider
  migration, corruption or tampering can be interpreted as plaintext instead of failing
  closed.
- **Fix**: Reject `plain` in production except under an explicit, observable migration
  flag. New writes must always use the primary authenticated provider; malformed MSEH
  envelopes must never fall back to plaintext.

#### F-H12 — Attachment stream encryption is malleable

- **Location**: `internal/plugin/encrypt/dek/stream.go`, reused by DEK, KMS, and Vault
  attachment paths.
- **Impact**: AES-CTR provides confidentiality but no authentication. A datastore or object
  storage attacker can flip chosen plaintext bits without detection, reorder/truncate
  content outside higher-level validation, and target every provider that reuses this
  stream implementation. The nonce contains only 64 random bits after the key identifier,
  which is also a weaker collision budget than the byte-encryption path.
- **Fix**: Implement the MSEH v3 sequential AES-256-GCM record format specified in
  [Authenticated attachment stream format](#authenticated-attachment-stream-format-f-h12).
  New writes use v3. V2 remains read-only behind an observable compatibility flag until a
  resumable migration verifies and rewrites it.

#### F-H13 — Active attachments can execute on the memory-service origin

- **Location**: `internal/plugin/route/attachments/attachments.go` accepts upload/source
  `Content-Type` and later serves it from the signed public download route; disposition is
  optional. The developer UI is served from the same origin.
- **Impact**: A user who can store or import `text/html`, SVG, or another active type can
  produce a signed same-origin URL. Opening it can execute script in the service origin,
  turning stored content into XSS and potentially exposing browser-held authorization
  state.
- **Fix**: Apply the server-enforced policy in
  [Attachment content isolation](#attachment-content-isolation-f-h13): omitted or unsafe
  dispositions download as `application/octet-stream`; only the specified raster image,
  audio, and video types may render inline. A separate origin is optional defense in depth,
  not a deployment requirement.

#### F-H14 — OAuth scopes and groups are treated as application roles

- **Location**: `internal/security/auth.go`, `extractTokenRoles`.
- **Impact**: Values from top-level `roles`, `groups`, OAuth `scope`, and
  `realm_access.roles` are merged. With the default admin role name `admin`, a valid token
  containing `scope=admin` can satisfy the memory-service administrator check even though
  OAuth scopes should constrain API access, not grant application roles.
- **Fix**: Use explicit, configurable claim paths for roles. Do not interpret `scope` as a
  role source; map groups only through an explicit policy. Add issuer-specific tests proving
  that scopes cannot grant admin access.

#### F-H15 — Manual release input is interpolated into shell source before validation

- **Location**: `.github/workflows/release.yml`, the step assigning
  `VERSION="${{ inputs.version }}"` before applying its version regex.
- **Impact**: A crafted `workflow_dispatch` value can break shell quoting and execute in a
  release job with write permissions and release secrets. Dispatch rights limit who can
  attempt this but do not make expression-to-shell interpolation safe.
- **Fix**: Pass the expression through a step-level `env` value, validate the environment
  variable as data, and use only the validated step output downstream. Add `actionlint` or
  `zizmor` checks for expression injection.

#### F-H16 — Fly deployment traces generated secrets to logs

- **Location**: `deploy/fly/deploy.sh` enables `set -x` while generating/exporting API,
  encryption, database, and integration secrets and invoking `fly secrets set`.
- **Impact**: Shell traces and the script's explicit output can disclose generated secrets
  to terminal history or CI logs, defeating secret storage at creation time.
- **Fix**: Never enable xtrace while secrets are in scope, do not print full credentials,
  minimize forwarded environment variables, and use the CLI's non-echoing input mechanism
  where available.

### Medium

#### F-M1 — CORS reflects arbitrary origins with credentials when enabled without origins

- **Location**: `internal/cmd/serve/cors.go`.
- **Impact**: Enabling CORS with an empty origin list selects wildcard behavior, reflects
  the request origin, and sends `Access-Control-Allow-Credentials: true`. This is a footgun
  for cookie-bearing proxies even though the service normally expects explicit auth headers.
- **Fix**: Require an explicit exact-origin list and reject wildcard-plus-credentials at
  startup.

#### F-M2 — OIDC audience is optional

- **Location**: `internal/security/auth.go` uses `SkipClientIDCheck: true` and checks
  audiences only when an allow-list is configured.
- **Impact**: A token for another application at the trusted issuer can be accepted by
  memory-service.
- **Fix**: In production, require at least one allowed audience and preferably an allowed
  client policy as well. Treat issuer-only trust as an explicit unsafe compatibility mode.

#### F-M3 — Raw internal errors reach API clients

- **Location**: route handlers under `internal/plugin/route`, auth middleware, and generated
  wrapper routes return many `err.Error()` values directly.
- **Impact**: Database messages, backend names, paths, and implementation detail can leak.
- **Fix**: Preserve intended 4xx validation detail, but map authentication/backend/5xx
  failures to stable public codes and log the detailed cause with a request identifier.

#### F-M4 — No service-side rate limiting

- **Location**: `internal/cmd/serve/server.go` middleware stack.
- **Impact**: Credential guessing, expensive search, attachment ingestion, and resource
  exhaustion are unthrottled when an ingress limiter is absent or bypassed.
- **Fix**: Use ingress limits as the primary control and add configurable coarse
  per-source/per-identity limits for direct exposure and expensive routes. Exempt or
  separately budget long-lived gRPC/SSE streams.

#### F-M5 — Dev credentials and datastore ports are broadly exposed **(dev/demo)**

- **Location**: [compose.yaml](../../compose.yaml).
- **Impact**: Known database, cache, object-store, monitoring, and tracing credentials are
  paired with host-published ports. Running on a non-private host exposes the stack.
- **Fix**: Bind dev ports to `127.0.0.1`, use generated local credentials, and document that
  the file is not a production baseline.

#### F-M6 — GitHub Actions are tag-pinned rather than commit-pinned

- **Location**: workflows under `.github/workflows`.
- **Impact**: A moved or compromised upstream tag changes executable CI code without a
  repository diff.
- **Fix**: Pin third-party actions to full commit SHAs and retain the human-readable version
  in comments; use dependency automation to refresh them.

#### F-M7 — Workflow token permissions are broader than individual jobs require

- **Location**: `.github/workflows/ci.yml` and release workflows.
- **Impact**: Matrix jobs inherit write capabilities such as package/check publication even
  when they only compile or test, increasing the effect of dependency or script compromise.
- **Fix**: Default workflows to `contents: read` and grant write scopes only on the jobs and
  steps that publish.

#### F-M8 — Langfuse credentials are literals in Compose **(dev/demo)**

- **Location**: [compose.yaml](../../compose.yaml), OTLP/Langfuse configuration.
- **Impact**: Copying the configuration can reuse known telemetry credentials and disclose
  traces containing prompts or user data.
- **Fix**: Generate or inject the credentials and apply the same local-only binding policy
  as other dev services.

#### F-M9 — Missing baseline browser security headers and TRACE rejection

- **Location**: main HTTP middleware and attachment responses.
- **Impact**: MIME confusion and active-content behavior are easier to exploit, and future
  frontend changes lack a consistent browser policy.
- **Fix**: Add `nosniff`, an appropriate referrer policy, HSTS only when the service owns
  TLS, targeted CSP for UI/attachment responses, and reject TRACE. Avoid a CSP that breaks
  generated developer assets without tests.

#### F-M10 — Forwarded headers and developer base URL lack an explicit trust policy

- **Location**: `internal/plugin/route/developer/developer.go` and Gin proxy configuration.
- **Impact**: A directly reachable service may trust attacker-supplied forwarded host/scheme
  values when creating redirects or absolute URLs.
- **Fix**: Apply [Proxy and external-origin policy](#proxy-and-external-origin-policy-f-m10):
  trust no proxy by default, configure explicit CIDRs only for client-IP resolution, and
  require a validated `MEMORY_SERVICE_BASE_URL` whenever the developer frontend runs outside
  testing. Forwarded host/proto never determine security-sensitive URLs.

#### F-M11 — HTTP server has only a read-header timeout

- **Location**: listener construction in `internal/cmd/serve`.
- **Impact**: Slow or idle connections consume resources longer than needed.
- **Fix**: Add header-size and idle limits plus route-aware body deadlines. Do not add a
  global write timeout that terminates gRPC, SSE, or response-recorder streams.

#### F-M12 — Management endpoints fall back to the public listener

- **Location**: `internal/cmd/serve/server.go` mounts management routes on the main router
  when no management listener is selected.
- **Impact**: Metrics and operational state can be public by omission.
- **Fix**: Require an explicit internal management listener in production or a clearly named
  opt-in for main-listener exposure.

#### F-M13 — Byte-encrypted records are not bound to row or purpose with AAD

- **Location**: DEK GCM helpers use `Seal`/`Open` with nil AAD; KMS and Vault reuse them.
- **Impact**: A datastore writer can swap otherwise valid ciphertext among compatible fields
  protected by the same provider/key. Schema checks may catch some swaps, but the crypto
  layer does not bind the value to its domain.
- **Fix**: Add a versioned envelope that authenticates the MSEH header and a stable domain
  label derived from record purpose and immutable identity. New writes use the new version;
  reads retain bounded old-version compatibility and rewrite on mutation/migration.

#### F-M14 — Production hardening is not validated centrally

- **Location**: defaults and checks spread across `internal/config` and
  `internal/cmd/serve`.
- **Impact**: Unsafe but syntactically valid combinations—plain encryption, wildcard CORS,
  issuer-only OIDC, public management routes, known dev secrets, or ambiguous proxy trust—
  can reach a listening state.
- **Fix**: Add one startup validation pass before any listener starts. Key it to the real
  `cfg.Mode`; use explicit per-risk migration/unsafe flags rather than a nonexistent
  `MEMORY_SERVICE_MODE` escape hatch.

#### F-M15 — Encryption documentation can leave operators on plaintext

- **Location**: [docs/encryption.md](../encryption.md).
- **Impact**: The guide uses the stale `MEMORY_SERVICE_ENCRYPTION_PROVIDERS` name instead of
  the current provider selector and describes stream encryption as authenticated AES-GCM.
  An operator can believe encryption is enabled while the service remains on the default
  `plain` provider, and can overestimate attachment integrity.
- **Fix**: Document canonical current variables, accepted key sizes, provider precedence,
  envelope versions, stream limitations/migration, and a startup signal that confirms the
  active primary provider without exposing key material.

#### F-M16 — TLS configuration still permits plaintext on the same listener by default

- **Location**: default listener configuration enables both TLS and plaintext/h2c.
- **Impact**: If a deployment exposes the listener directly, clients or intermediaries can
  send bearer/API-key traffic without transport encryption even when TLS is configured.
- **Fix**: Production validation should require TLS-only or an explicit
  `plaintext-behind-trusted-terminator` policy with bind-address/proxy constraints. Keep
  dual-mode behavior only where the deployment boundary is documented and tested.

#### F-M17 — Frontends open unvalidated attachment URLs

- **Location**: developer `HistoryRenderer.tsx` and chat frontend conversation attachment
  rendering pass `attachment.href` to anchors/`window.open` without a scheme allow-list.
- **Impact**: Content from an untrusted/shared conversation can supply `javascript:`,
  `data:`, or misleading external URLs. Browser behavior varies by element and invocation,
  but the clients should not treat arbitrary URI schemes as trusted navigation.
- **Fix**: Accept only HTTPS/HTTP and safe same-origin relative URLs, reject active schemes,
  and use `noopener,noreferrer` for new windows.

### Low / Informational

#### F-L1 — Delve debug port can be host-published **(dev)**

- Bind the commented/debug configuration to loopback and never enable it on a shared host.

#### F-L2 — Docker build context excludes too little

- **Location**: `.dockerignore` excludes only one of several ignored local artifact patterns.
- **Impact**: Local logs, screenshots, credentials, or caches can be sent to a remote Docker
  builder and copied into the builder stage even though they are not tracked by Git.
- **Fix**: Align `.dockerignore` with local artifact, secret, VCS, editor, and build-output
  patterns; verify the runtime stage contains only intended files.

#### F-L3 — SHA-1 is used only for non-security identifiers

- **Location**: turn-trace deduplication and PostgreSQL advisory-lock key derivation.
- **Disposition**: No security change required; optional comments can suppress scanner noise.

#### F-L4 — OIDC certificate verification bypass is explicit and opt-in

- **Location**: `OIDCTLSSkipCertificateVerify` handling in `internal/security/auth.go`.
- **Disposition**: Keep it prohibited by production validation and out of production examples.

#### F-L5 — Fly/docs reference the removed attachment signing secret

- **Location**: `deploy/fly/deploy.sh`, `deploy/fly/README.md`, and historical enhancement
  text. The active signing key is provider-derived.
- **Fix**: Remove the dead setting from active scripts/docs; do not rewrite historical design
  records unless they claim to describe current configuration.

#### F-L6 — PostgreSQL dev outbox uses weak credentials on a published port **(dev)**

- Bind to loopback, generate credentials, and keep logical-replication access internal.

#### F-L7 — Envoy uses host networking in kind configuration **(dev)**

- Keep this confined to kind/local overlays; production should use normal service exposure.

#### F-L8 — CI path filters omit security-relevant workflow/devcontainer changes

- **Location**: `.github/workflows/ci.yml` ignores `.github/**` and misspells
  `.devcontainer`; the devcontainer workflow does not watch all effective configuration.
- **Fix**: Correct the path and ensure workflow/devcontainer security changes run an
  appropriate static/build check.

#### F-J1 — Java production modules need a focused client-library audit

- **Location**: `java/quarkus` and `java/spring` production modules.
- **Disposition**: The scan found no Java server persistence/resource surface matching the
  original draft and no production hardcoded secret. This is a separate follow-up audit,
  not an implementation task or readiness dependency of this enhancement. That audit should
  cover authorization-header propagation, redirect behavior, TLS, Unix-socket proxying,
  exception logging, and generated-client defaults. Demo `permitAll`/CSRF-disabled examples
  must remain clearly labeled as demos.

## Existing Strengths

- Byte encryption uses fresh random nonces with AES-GCM; provider key rotation and
  HKDF-SHA256 token-key separation are implemented. This statement intentionally excludes
  the AES-CTR stream path in F-H12.
- Attachment source fetching validates scheme/host, blocks loopback/private/link-local
  destinations, and revalidates at dial and post-connect time, substantially reducing DNS
  rebinding SSRF.
- Attachment tokens use HMAC-SHA256, constant-time verification, expiry, and signing-key
  rotation; verification fails closed without a provider signing key.
- Conversation authorization is enforced in store queries and handlers check returned
  access levels. No IDOR or injectable SQL/NoSQL construction was found in reviewed paths.
- Membership operations cap role changes and keep ownership transfer separate.
- Authentication fails closed when no mechanism is configured in production mode and
  rejects invalid credentials rather than silently downgrading them.
- The React clients do not use `dangerouslySetInnerHTML`; URL handling remains in scope via
  F-H13 and F-M17.
- Workflows do not use `pull_request_target`; image publication is branch-gated and release
  secrets use a protected environment. F-H15 still applies to manual release input.
- Global request-body limits are already present.

## Proposed Changes

### Resolved cross-cutting decisions

1. Production OIDC must validate an intended audience; issuer-only trust requires an
   explicit compatibility flag.
2. Plaintext fallback is migration-only, observable, and never applies to malformed MSEH.
3. AAD rollout uses a new envelope version with backward reads; persisted data is not reset.
4. Rate limiting is layered: ingress is primary, while the service supplies configurable
   protection for direct exposure and expensive endpoints.
5. HTTP limits are route-aware so ordinary request bodies receive deadlines without breaking
   gRPC/SSE/response-recorder streams.
6. Unsafe configuration is rejected through explicit flags tied to each risk; runtime mode
   is not exposed as a generic environment-variable bypass.
7. Encrypted attachments remain sequential streams. The current attachment API and store
   SPI expose `io.Reader` and do not support HTTP Range requests, so v3 does not add random
   access.
8. Attachment responses are download-by-default. A separate attachment origin is optional
   defense in depth rather than a prerequisite for secure deployment.
9. The developer frontend uses an explicit configured external origin in production;
   forwarded host/proto values are not an origin-discovery mechanism.
10. S3 inline attachments are proxied so the service can apply CSP, `nosniff`, and referrer
    headers; only forced-download responses may use direct presigned object URLs.
11. Attachment migration refuses missing/malformed SHA-256 metadata; it does not treat
    unauthenticated v2 plaintext as its own integrity baseline.
12. The devcontainer retains isolated privileged DinD for integration tests but removes the
    host-home/private-key and unused SOCKS exposures; the remaining risk is documented as
    trusted-workspace, development-only.
13. The focused Java client audit is separate follow-up work, not a hidden dependency of this
    enhancement's Ready status.
14. The shared error contract intentionally changes `SearchTypeUnavailableError.error` from
    a machine code to a message while preserving deprecated aliases for one release.

### Authenticated attachment stream format (F-H12)

Use **MSEH version 3** with fixed-size sequential AES-256-GCM records. AES-GCM is already
implemented by every real provider and avoids adding another cryptographic dependency.
[RFC 5116](https://www.rfc-editor.org/rfc/rfc5116.html) requires nonce uniqueness for a
given key and unambiguous associated-data encoding; the v3 construction makes the stream
key unique per attachment and the record nonce unique within that stream. Go's
[`cipher.AEAD`](https://pkg.go.dev/crypto/cipher#AEAD) supplies the required authenticated
seal/open primitive and 16-byte GCM tag.

#### Header and key derivation

- `EncryptionHeader.version = 3`.
- `provider_id` keeps its existing value (`dek`, `kms`, or `vault`).
- The existing `nonce` field is exactly 24 bytes: the existing 8-byte SHA-256-derived key ID
  followed by a fresh 16-byte random stream salt. No protobuf schema change is required.
- Select the master DEK by key ID, then derive a 32-byte per-stream AES key with HKDF-SHA256:
  input key material is the provider DEK, salt is the 16-byte stream salt, and info is the
  exact ASCII string `memory-service/mseh/v3/attachment-stream-key`.
- A record's 12-byte GCM nonce is `salt[0:8] || uint32_be(record_index)`. The per-stream
  derived key changes with the 128-bit salt; the counter makes every nonce under that key
  distinct. Reject streams that would require record index `2^32`.

#### Record framing

The plaintext chunk size is fixed at 64 KiB. Keeping it fixed limits memory, yields modest
tag/framing overhead, and avoids a remotely configurable allocation field. A future format
can use a new MSEH version if random access or a different chunk size becomes necessary.

```text
DATA  record: 0x00 | uint32_be(plaintext_length) | GCM(ciphertext || 16-byte tag)
FINAL record: 0x01 | uint32_be(0)                | 16-byte GCM tag for empty plaintext
```

- DATA lengths are `1..65536`. Writers emit full records except for the final DATA record;
  an empty attachment contains only FINAL.
- Record index is implicit, starting at zero. FINAL uses the next index after the last DATA
  record.
- Associated data is a canonical, length-delimited binary encoding of the domain string
  `memory-service/mseh/v3/attachment-stream`, provider ID, version, 24-byte header nonce,
  record type, record index, plaintext length, and total plaintext length. The total length
  is zero for DATA and the observed total for FINAL.
- Decryption authenticates each DATA record before releasing that record's plaintext.
  Reordering, duplication, length changes, and bit flips therefore fail at the affected
  record.
- A valid stream must contain exactly one authentic FINAL record followed immediately by
  EOF. Missing FINAL detects truncation; data after FINAL is rejected.
- Once a short DATA record is observed, the next record must be FINAL. Zero-length DATA,
  unknown record types, oversized lengths, counter overflow, and trailing bytes are errors.
- The encrypted attachment route rejects Range requests with `416`; v3 is sequential and
  does not claim authenticated random access.
- Callers must propagate decryption errors. In particular, `streamAttachment` must stop
  discarding `io.Copy` errors. Earlier authenticated chunks may already have been written
  to an HTTP response before a later corruption is detected, so consumers must treat a
  shortened/erroring response as failed.

#### V2 compatibility and migration

- Mixed-version/rolling deployments are not supported. Upgrade by stopping every old
  memory-service replica before starting the new binary. The new binary reads v2 and writes
  v3 immediately.
- Add `--encryption-legacy-stream-v2-read-enabled` /
  `MEMORY_SERVICE_ENCRYPTION_LEGACY_STREAM_V2_READ_ENABLED`. It defaults to `true` and gates
  only v2 reads; v2 is never written again.
- Each v2 read increments
  `memory_service_encryption_legacy_stream_reads_total{version="2"}` and emits a rate-limited
  warning. Production validation warns while compatibility is enabled.
- Add resumable `memory-service migrate attachments --to-stream-version=3`, with `--dry-run`
  to inventory versions and metadata problems without writing. It pages through
  admin attachment metadata and skips non-v2 objects. The encrypted-store wrapper reads the
  old object directly from its inner store, decrypts v2, hashes the plaintext while streaming
  it through the v3 writer into a `0600` local **ciphertext** temp file, and compares the hash
  with attachment metadata before replacement. Plaintext is never spooled to disk.
- Preserve the existing `memory-service migrate` database-migration action and flags. Add
  `attachments` as a child command rather than repurposing the current command. The child
  command must accept the same datastore, attachment-store, and encryption-provider
  configuration sources needed by `serve`; adding the child must not break existing
  `memory-service migrate --db-url ...` automation.
- Add an optional `AtomicAttachmentReplacer` capability with
  `Replace(ctx, storageKey, data, contentType)`. Built-in backends implement it: filesystem
  writes a same-directory temp and renames, PostgreSQL replaces the large object/chunks in a
  transaction, and S3 completes a `PutObject` to the same key. SQLite and MongoDB attachment
  deployments use filesystem or S3 storage and therefore need no database-specific replacer.
  Keeping the capability separate from `AttachmentStore` avoids a compile-time break for
  external attachment plugins; the migrator fails before changing data when the selected
  backend lacks it.
- Existing metadata with a missing or malformed SHA-256 fails migration and leaves the object
  unchanged. There is no flag that blesses the current unauthenticated v2 plaintext as its own
  integrity baseline. Operators must restore a trusted SHA-256 from an upload manifest/backup,
  or delete and re-upload the attachment, before rerunning migration.
- Only after hash verification does migration replace the old object at its existing storage
  key; attachment metadata does not change. A failed hash or interrupted write leaves the
  old object intact. Startup/migration cleanup removes stale
  `memory-service-reencrypt-*` ciphertext temp files.
- Operators disable v2 reads only after the migrator reports zero remaining v2 objects and
  the read metric stays at zero for their normal retention window. Removing the decoder or
  changing the default to `false` requires separate evidence that supported installations no
  longer contain v2 data.
- Do not rewrite on ordinary reads: a caller may stop early, and the current retrieve API has
  no safe atomic replacement contract.

### Attachment content isolation (F-H13)

Normalize stored media types with `mime.ParseMediaType` and lowercase the base type. Invalid
or missing types become `application/octet-stream`. Content bytes are not trusted merely
because the uploader supplied an allowed type; `nosniff` and the response override prevent
HTML mislabeled as an image from being reparsed as active content.

The only safe-inline types are:

```text
image/avif     image/gif      image/jpeg     image/png      image/webp
audio/mpeg     audio/ogg      audio/wav      audio/webm
video/mp4      video/ogg      video/webm
```

SVG, PDF, every `text/*` type, XML, JSON, JavaScript, multipart content, and every unlisted
type are download-only. Expanding the list requires a security review and route/browser
tests; it is not user-configurable.

| Requested disposition | Stored type | Effective response |
|---|---|---|
| `inline` | allow-listed | `Content-Disposition: inline` and canonical stored type |
| `inline` | invalid/unlisted | `Content-Disposition: attachment` and `application/octet-stream` |
| `attachment` | any | `Content-Disposition: attachment`; use canonical type only when allow-listed, otherwise `application/octet-stream` |
| omitted | any | Same as `attachment` |

Apply the policy at the final serving/signing boundary, not only at upload, so existing rows,
source-URL imports, admin downloads, token downloads, and a modified disposition query cannot
bypass it. Proxy-served responses also set:

```text
X-Content-Type-Options: nosniff
Content-Security-Policy: sandbox; default-src 'none'; base-uri 'none'; form-action 'none'
Referrer-Policy: no-referrer
```

Build `Content-Disposition` with `mime.FormatMediaType` rather than string interpolation.
[S3 presigned `GetObject` responses](https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetObject.html)
can override content type and disposition but cannot supply the required CSP, referrer policy,
or `nosniff` header. Therefore every requested `inline` S3 response is proxied through
memory-service. Direct presigned S3 responses are allowed only for the effective
`attachment` disposition, with both response content disposition and response content type
overridden and `application/octet-stream` used for unlisted content. If either override
cannot be guaranteed, proxy the download.

A distinct cookieless attachment origin remains recommended defense in depth for deployments
that can provide it, but it is not required for this enhancement: the fixed server-side list,
download default, proxy-only S3 inline path, `nosniff`, and sandbox CSP close the identified
same-origin execution path without adding mandatory DNS/TLS/deployment machinery.

### Proxy and external-origin policy (F-M10)

Add one configuration field and reuse the existing base URL:

| Purpose | CLI | Environment | Default |
|---|---|---|---|
| Trusted TCP proxy addresses | `--trusted-proxy-cidrs` | `MEMORY_SERVICE_TRUSTED_PROXY_CIDRS` | empty (trust none) |
| Browser-facing service origin | `--base-url` | `MEMORY_SERVICE_BASE_URL` | required with production developer frontend |

- Store trusted proxies as a comma-separated config string, parse each item as an exact IP or
  CIDR, and call `router.SetTrustedProxies(nil)` when empty. This explicitly replaces Gin's
  trust-all default. Reject invalid entries and all-address ranges such as `0.0.0.0/0` and
  `::/0` during startup validation.
- When configured, pass only the parsed values to `SetTrustedProxies`. They affect
  `Context.ClientIP()` for access logs and rate limiting; they do not authorize requests and
  do not supply the developer frontend's external origin.
- When the developer frontend is enabled outside `ModeTesting`, `BaseURL` is required. It
  must be an absolute URL with a non-empty host, no userinfo, query, or fragment, and no path
  other than `/`. Require `https`; allow `http` only when the hostname is `localhost` or a
  loopback IP so the existing local Compose flow remains usable. Normalize it to an origin
  without a trailing slash.
- Remove `X-Forwarded-Proto` and `X-Forwarded-Host` from `resolveBaseURL`. In testing only,
  an unset base URL may fall back to `Request.TLS` plus the request `Host` so dynamic
  Testcontainers ports continue to work; forwarded headers remain ignored.
- Direct TCP requests outside a configured proxy CIDR use the socket peer address even if
  they send forwarding headers.
- Unix-socket requests never trust forwarded client identity. Use authenticated identity or
  peer credentials for authorization/rate-limit keys, and use the explicit base URL for
  browser redirects/config. A reverse proxy that reaches memory-service through a Unix socket
  therefore does not need a special trust-all mode.
- Apply the same explicit trust-none initialization to management routers. Management
  listeners do not consume forwarded headers.

### Dev/demo containment (F-H4–F-H9/F-M5/F-M8)

Local assets remain convenient without being reusable as exposed deployments:

- Compose host ports bind to `127.0.0.1`; local credentials are generated into an ignored
  environment file by the documented bootstrap task rather than committed as literals.
- Grafana anonymous access is disabled and its generated administrator credential is shown
  once by the bootstrap task. Prometheus, Grafana, and management endpoints have no public
  demo-gateway `HTTPRoute`.
- The devcontainer no longer mounts the host home, copies private SSH material, starts
  microsocks, or publishes port 1080. It relies on SSH-agent forwarding and narrowly scoped
  non-secret Git/host-key configuration.
- Docker-in-Docker stays isolated from the host Docker daemon and is pinned to an exact
  devcontainer-feature release. Its privileged outer-container requirement is accepted only
  for trusted local development because Compose-based integration tests depend on it; the
  devcontainer documentation states that untrusted repositories/branches must not be opened
  in this environment.

No production guide or Kubernetes base imports values from these dev/demo assets.

### Compatibility and coordinated rollout

This enhancement contains intentional security-breaking behavior but does not remove REST
routes, OpenAPI fields, or reset persisted data. Deployment and API documentation must call
out these changes rather than presenting the work as transparent hardening.

| Change | Compatibility impact | Required rollout action |
|---|---|---|
| MSEH v3 attachment writes | Old binaries cannot read attachments written or migrated to v3 | Stop all old replicas before upgrade; take a metadata/object backup; rollback after any v3 write requires restoring that backup |
| V2 attachment migration | Rewritten objects are forward-only for binaries without v3 support | Validate the new binary first, run the resumable migrator, verify zero v2 objects, then disable v2 reads |
| MSEH v4 field writes | Old binaries cannot authenticate fields bound to v4 AAD | Stop old replicas, back up every datastore, enable v1/plain reads only as needed, migrate all four field domains, then disable compatibility reads |
| Attachment response policy | Omitted disposition now downloads; unsafe/unlisted types cannot render inline | Update OpenAPI descriptions, client/docs examples, and release notes; callers that need preview must request `inline` and use an allow-listed type |
| S3 inline delivery | Inline S3 attachments are proxied instead of returned as presigned object URLs | Size proxy capacity for inline media; attachment downloads may remain direct when response overrides are enforceable |
| OIDC role/audience policy | Tokens that relied on `scope`, top-level `roles`, implicit groups, or lack an accepted audience are rejected | Configure JSON Pointer role claims and audience/client allow-lists before upgrade; there is no scope-role compatibility mode |
| Production startup validation | Plain encryption, wildcard credentialed CORS, public management fallback, ambiguous TLS/plaintext, missing frontend base URL, unsafe proxy trust, or demo secrets can stop startup | Validate configuration in staging and set only the documented risk-specific migration/unsafe flags |
| Listener bind defaults | TCP listeners default to loopback rather than all interfaces | Set explicit container/Kubernetes hosts and the trusted-terminator/internal-management acknowledgements |
| Trust-none proxy default | Unconfigured proxy deployments see the immediate proxy as client IP | Configure exact trusted TCP proxy CIDRs before relying on client-IP logging or rate limiting |
| Non-root/read-only container | Implicit writes to the image filesystem fail | Mount explicitly writable attachment/temp paths with the runtime UID/GID before deployment |
| Disabled service-account token | Undocumented Kubernetes API access stops working | Confirm no external integration depends on the pod token; repository code has no such dependency |
| Sanitized errors and rate limits | Error bodies gain required codes/request IDs, `SearchTypeUnavailableError.error` changes from code to message, and clients may receive `429` | Branch on `code` instead of `error`, accept deprecated search aliases for one release, propagate request IDs, and implement `Retry-After`/gRPC `RetryInfo` handling |

The supported storage upgrade sequence is:

1. stop all memory-service replicas and take a consistent database plus attachment-object
   backup;
2. deploy the new binary, which reads v1/v2 and writes v4/v3 respectively, with required
   legacy reads enabled;
3. verify API, attachment, authentication, management-listener, and proxy behavior;
4. run `memory-service migrate attachments --to-stream-version=3` until it reports no v2
   objects, then run `memory-service migrate encryption-fields --to-version=4` until every
   field domain reports zero legacy values;
5. observe zero v1, v2, and legacy-plain reads for the installation's normal retention
   window; and
6. disable all unneeded compatibility reads and retain the pre-upgrade backup according to
   the rollback policy.

Rollback to an old binary is supported only before the new binary writes v3 or v4. After the
first new-version write or migration replacement, rollback requires restoring the coordinated
pre-upgrade database/object backup. Mixed old/new processes are explicitly unsupported and
are not a test target.

### Operator security documentation

Create `site/src/pages/docs/deployment/security.mdx`, routed as
`/docs/deployment/security/`, with title **Security and Deployment Hardening**. It is the
operator-facing source of truth; this enhancement remains the engineering findings and
implementation record.

The guide covers:

- OIDC audience/client restrictions, explicit role claims, API-key generation and rotation;
- DEK/KMS/Vault selection, prohibition of production `plain`, key rotation, attachment v2
  migration, backup requirements, and the forward-only rollback boundary;
- TLS-only operation versus an explicitly trusted TLS terminator, trusted proxy CIDRs,
  required external frontend base URL, and CORS origin policy;
- local-versus-cluster rate limits, stable error/request-ID behavior, and client `429`
  handling;
- a dedicated internal management listener, protected metrics/monitoring, and datastore,
  object-store, vector-store, and cache network boundaries;
- attachment download defaults, inline MIME allow-list, signed URL behavior, S3 response
  overrides, proxy-only inline S3 behavior, and private-source-URL risk;
- non-root/read-only container operation, writable attachment/temp volumes, Kubernetes
  security context, and disabled service-account token mounting;
- secret generation/storage, prohibition of known Compose/demo values, log/xtrace handling,
  and a final deployment verification checklist.

The site guide does **not** repeat finding IDs, severities, source-code locations, exact MSEH
binary framing, CI/devcontainer maintainer work, or internal migration implementation. It
links to configuration, encryption, attachment, Docker, and Unix-socket documentation and
publishes flags/defaults only in the same change that implements them.

Re-enable the currently commented **Deployment** sidebar section with only pages that exist:

1. Docker (`/docs/deployment/docker/`); and
2. Security Hardening (`/docs/deployment/security/`).

Do not expose the sidebar's currently nonexistent Kubernetes or database pages. Refresh the
existing Docker page so its production example uses an immutable image, generated secrets,
authentication, real encryption, TLS or an explicit trusted terminator, an internal
management listener, writable non-root volumes, and current single-port behavior. Update the
configuration page for implemented flags/defaults and changed attachment semantics, and link
the security guide from the Docker page, configuration page, and FAQ production-readiness
notice.

### Workstream boundaries

The work can land in independent tracks:

1. container/Kubernetes and immutable-image hardening (F-H1, F-H2, F-H10);
2. authenticated encryption and production config validation (F-H3, F-H11, F-H12,
   F-M13–F-M16);
3. attachment serving and frontend navigation safety (F-H13, F-M9, F-M17);
4. OIDC/auth/error/rate/proxy hardening (F-H14, F-M1–F-M4, F-M10–F-M12);
5. CI/release/deployment secret safety (F-H15, F-H16, F-M5–F-M8, F-L2, F-L8);
6. dev/demo containment (remove public monitoring routes, host-home/SSH copying, and
   microsocks; retain documented isolated DinD) plus documentation cleanup; and
7. operator security guidance and deployment-document correction.

### OIDC role claims and token restrictions (F-H14/F-M2)

Claim-derived application roles use [RFC 6901 JSON Pointer](https://www.rfc-editor.org/rfc/rfc6901.html)
paths. Add repeatable
`--oidc-role-claim` flags and `MEMORY_SERVICE_OIDC_ROLE_CLAIMS`, whose environment value is a
JSON array of JSON Pointer strings. When neither is set, the only default path is
`/realm_access/roles`, preserving the repository's Keycloak configuration without trusting
top-level groups or scopes. An explicitly configured empty array disables all claim-derived
roles; static user/client role mappings continue to apply.

For each configured pointer:

- a missing claim contributes no roles;
- `~0` and `~1` are decoded as required by RFC 6901, and an invalid pointer fails startup;
- a string contributes exactly one role and is never split on whitespace;
- an array must contain only strings; any other present value makes the token invalid;
- role values are trimmed, non-empty, case-sensitive, and deduplicated; and
- accept at most 16 paths, 256 resulting roles, and 256 UTF-8 bytes per role.

`scope` remains available only to the existing permission-scope gates. `groups` contributes
roles only when the operator explicitly includes `/groups`. Do not read top-level `roles`,
`groups`, or any other implicit source. The service supports one configured issuer, so one
claim-path list applies to that issuer; multi-issuer mappings are a non-goal.

When `OIDCIssuer` is configured, `OIDCAllowedAudiences` must contain at least one value.
Audience matching succeeds when any string in the token's `aud` claim exactly matches the
configured set. `OIDCAllowedClients`, when non-empty, independently requires an exact `azp`
or `client_id` match. Missing required claims reject the token. A temporary
`--oidc-allow-missing-audience` /
`MEMORY_SERVICE_OIDC_ALLOW_MISSING_AUDIENCE=true` compatibility flag permits issuer-only
validation, emits a startup warning and
`memory_service_security_unsafe_config{reason="oidc_missing_audience"} 1`, and is never set
by production examples. There is no compatibility mode that restores scope-to-role mapping.

### Stable public error contract (F-M3)

Keep the existing JSON envelope names so current clients continue to deserialize it, but
make `code`, `error`, and `requestId` present on every non-streaming REST error generated
after a request reaches application middleware:

```json
{
  "code": "validation_error",
  "error": "page size must be between 1 and 1000",
  "requestId": "6d9beac0-8485-4c7b-b84f-0c442c21eafe",
  "details": {
    "field": "pageSize"
  }
}
```

`error` is a safe human-readable message, not a branching contract. `details` is optional
and may contain only handler-selected primitives, arrays, or objects; it never contains a
wrapped error, SQL, storage key, path, host, token, or upstream response body. Retain the
currently used top-level `field` property as deprecated compatibility output when applicable,
while also returning `details.field`.

The current conversation-search `SearchTypeUnavailableError` is the one incompatible legacy
shape: it uses `error` as the machine code and a separate `message`. During this intentionally
breaking transition it returns the shared `code`, human-readable `error`, `requestId`, and
`details.availableTypes`, while retaining deprecated top-level `message` and
`availableTypes` aliases for one release. No other existing field is removed.

The initial stable code registry is:

| HTTP | Code | gRPC | Public message policy |
|---:|---|---|---|
| 400 | `invalid_request`, `validation_error` | `InvalidArgument` | Preserve typed field/range/format guidance |
| 401 | `unauthenticated` | `Unauthenticated` | Generic authentication failure; no verifier detail |
| 403 | `permission_denied` | `PermissionDenied` | Name the required service permission/role, not token contents |
| 404 | `not_found` | `NotFound` | Name the resource type; do not expose backend lookup detail |
| 405 | `method_not_allowed` | `Unimplemented` | Name the unsupported operation |
| 408 | `request_timeout` | `DeadlineExceeded` | Generic request-body timeout |
| 409 | `conflict` | `Aborted` | Preserve typed revision/state conflict guidance |
| 413 | `payload_too_large` | `ResourceExhausted` | Include the configured public byte limit |
| 415 | `unsupported_media_type` | `InvalidArgument` | Name only the accepted media-type constraint |
| 422 | `validation_error` | `InvalidArgument` | Preserve typed semantic validation guidance |
| 429 | `rate_limited` | `ResourceExhausted` | Include `details.retryAfterSeconds` |
| 500 | `internal_error` | `Internal` | Always `internal server error` |
| 501 | `not_implemented`, `search_type_unavailable` | `Unimplemented` | Name the unavailable service capability |
| 502 | `upstream_error` | `Unavailable` | Name only the unavailable upstream capability |
| 503 | `service_unavailable` | `Unavailable` | Name only the unavailable service capability |
| 504 | `upstream_timeout` | `DeadlineExceeded` | Name only the timed-out upstream capability |

Existing domain codes such as `search_type_unavailable` remain valid after being registered
in the shared mapper. New codes require an OpenAPI change and tests. Unknown or untyped errors
map to `internal_error`; raw `err.Error()` is public only for a typed public validation or
conflict error.

TLS failures, malformed HTTP request lines/headers, and header-limit rejection can occur in
`net/http` before application middleware and therefore use the transport's generic error
instead of the JSON envelope/request ID. They must still contain no application or backend
detail. This is the only REST-envelope exception.

A request-ID middleware runs before recovery and authentication. It accepts an inbound
`X-Request-ID` only when it matches `[A-Za-z0-9._-]{1,128}`; otherwise it generates a random
UUID. The ID is returned in the `X-Request-ID` header and JSON body and included in every
access/error log. gRPC uses canonical status codes and safe messages, returns the ID in
`x-request-id` initial/trailing metadata, and never embeds an internal Go error. Streaming
failures that occur after headers use the protocol's terminal status/trailer rather than
trying to append JSON.

### Local service rate limits (F-M4)

Add process-local token buckets with `--rate-limit-mode=local|off` /
`MEMORY_SERVICE_RATE_LIMIT_MODE`; the default is `local`. `off` is an explicit operator
choice and emits a startup warning plus
`memory_service_security_unsafe_config{reason="rate_limits_off"} 1`. Local limiting avoids
making Redis/Infinispan a service-availability dependency. Its effective aggregate rate
scales with replica count; the operator guide therefore requires ingress/gateway limits for
cluster-wide enforcement.

Each class uses the exact grammar `<tokens>/<duration>,burst=<tokens>` and has these defaults:

| Class | CLI / environment | Default |
|---|---|---|
| Source admission | `--rate-limit-source` / `MEMORY_SERVICE_RATE_LIMIT_SOURCE` | `600/1m,burst=100` |
| Authenticated identity | `--rate-limit-identity` / `MEMORY_SERVICE_RATE_LIMIT_IDENTITY` | `1200/1m,burst=200` |
| Authentication failures | `--rate-limit-auth-failure` / `MEMORY_SERVICE_RATE_LIMIT_AUTH_FAILURE` | `30/1m,burst=10` |
| Expensive operations | `--rate-limit-expensive` / `MEMORY_SERVICE_RATE_LIMIT_EXPENSIVE` | `60/1m,burst=10` |
| Stream opens | `--rate-limit-stream-open` / `MEMORY_SERVICE_RATE_LIMIT_STREAM_OPEN` | `30/1m,burst=5` |

Zero/negative rates, zero bursts, unknown syntax, or durations outside `1s..1h` fail startup.
Buckets expire after 15 minutes idle and the cache is bounded to 100,000 keys per class.
At capacity, evict the least-recently-seen bucket before admitting a new key; source admission
limits the rate at which untrusted identity churn can force eviction.

- Source admission runs before authentication on all main-listener HTTP/gRPC requests. HTTP
  uses Gin `Context.ClientIP()`; gRPC uses the same shared peer/trusted-CIDR resolver. Both
  ignore forwarded addresses unless the immediate TCP peer is trusted under F-M10.
- For Unix-socket listeners, source admission keys by peer UID when the platform exposes
  peer credentials; otherwise all connections on that socket share one stable listener key.
  Authenticated identity buckets still distinguish callers after authentication. Deployments
  that reverse-proxy many users through a Unix socket must apply source admission at ingress.
- Authenticated identity limiting runs after authentication. Its key is credential kind,
  user ID, and client ID; absent components remain explicit empty segments. API-key-only
  identities therefore key primarily by client ID.
- Before authentication, middleware rejects a source whose authentication-failure bucket is
  already empty. A failed authentication then consumes that bucket in addition to the source
  admission token; successful authentication does not consume a failure token.
- Search, attachment upload/source import, download-URL issuance, and admin maintenance or
  eviction consume both the identity and expensive buckets.
- Opening SSE, response-recording, or any gRPC stream consumes source, identity, and
  stream-open buckets once. Bytes/messages on an accepted long-lived stream are not charged;
  existing stream concurrency/buffer limits continue to apply.
- Dedicated management health/readiness/metrics routes are exempt from token buckets but
  remain subject to the listener limits below. When management routes are explicitly mounted
  on the main listener, source admission applies. Developer static assets consume only source
  admission tokens.

No request is queued. Following [RFC 6585](https://www.rfc-editor.org/rfc/rfc6585.html), REST
rejection returns `429`, integer `Retry-After` seconds, and the
stable `rate_limited` error. gRPC returns `codes.ResourceExhausted` with a
`google.rpc.RetryInfo` detail. Emit accepted/rejected counters by route class, never by raw
user, client, or IP label.

### Startup security validation and transport policy (F-H11/F-M1/F-M12/F-M14/F-M16)

Run one validation pass after all flags/plugin configuration is applied and before opening a
database, object store, background worker, or listener. It applies whenever
`cfg.Mode != ModeTesting`; `ModeTesting` remains programmatic and is not exposed as an
environment escape hatch. Validation returns all detected problems in one error.

| Unsafe condition | Decision |
|---|---|
| Primary encryption provider is `plain` | Fail unless `--encryption-allow-plain` / `MEMORY_SERVICE_ENCRYPTION_ALLOW_PLAIN=true`; the flag warns and sets unsafe-config telemetry |
| Headerless legacy byte data may be plaintext | Read only with `--encryption-legacy-plain-read-enabled`; default `false`; malformed MSEH always fails |
| MSEH v1 byte values | Read with `--encryption-legacy-byte-v1-read-enabled`; default `true`; warn/metric until migrated |
| MSEH v2 attachment streams | Governed by the default-true v2 read flag and migration specified above |
| OIDC issuer without allowed audience | Fail unless the audience compatibility flag above is explicit |
| OIDC TLS verification bypass | Fail outside `ModeTesting`; install the issuer CA instead |
| CORS enabled with no exact origins, `*`, `null`, or an origin containing path/query/fragment | Fail with no unsafe override |
| Management routes lack a dedicated listener | Fail unless `--management-on-main-listener` / `MEMORY_SERVICE_MANAGEMENT_ON_MAIN_LISTENER=true`; the opt-in warns and sets telemetry |
| Any TCP listener enables both TLS and plaintext | Fail with no unsafe override |
| Plaintext TCP binds beyond loopback | Fail unless `--plaintext-behind-trusted-terminator` is true; the opt-in requires non-empty trusted proxy CIDRs, warns, and must be paired with an ingress/network boundary in docs |
| Developer frontend lacks a valid explicit base URL | Fail under the F-M10 policy |
| Proxy CIDRs are invalid/universal | Fail under the F-M10 policy |
| A known repository demo secret is present in a service-owned config value | Fail by exact-value denylist for values the process can observe, including API keys, DEKs, database URL credentials, and configured backend API keys; local assets must generate/inject values rather than bypass this check |

Compose/deployment assets separately remove or generate credentials for external services
that the memory-service process cannot observe, such as Grafana or Langfuse. Startup
validation must not claim to validate secrets it never receives.

Add `--host` / `MEMORY_SERVICE_HOST` and `--management-host` /
`MEMORY_SERVICE_MANAGEMENT_HOST`, both defaulting to `127.0.0.1`; TCP listeners bind the
configured address instead of `:port`. Container/Kubernetes examples explicitly use
`0.0.0.0` only with the appropriate trusted-terminator or internal-management boundary.
Plaintext over an absolute Unix socket is allowed. Generated self-signed TLS is limited to
loopback/testing; non-loopback TLS requires configured certificate/key files.

The risk-specific compatibility flags are intentionally independent—there is no single
“allow insecure” switch. Every accepted unsafe flag is logged once without secret values and
exported through the bounded `memory_service_security_unsafe_config{reason}` gauge. The site
guide documents removal steps. Local Task/Compose examples must either configure the safe
control or explicitly opt into only the local risk they need.

### MSEH v4 field binding and migration (F-M13)

Use **MSEH version 4** for byte-encrypted persisted fields. The existing version 1 remains
read-only during migration; version 3 remains reserved for authenticated attachment streams.
V4 keeps a fresh 12-byte AES-GCM nonce and adds canonical AAD binding to the exact envelope
header, field purpose, and immutable record identity.

Define `frame(x)` as `uint32_be(len(x)) || x`. The exact AAD is:

```text
frame("memory-service/mseh/v4/field") ||
frame(raw_mseh_header_prefix) ||
frame(domain_label) ||
frame(immutable_identity)
```

`raw_mseh_header_prefix` is the exact on-wire MSEH magic, encoded protobuf-length varint, and
protobuf bytes written before the ciphertext. Domain labels and identities are UTF-8 bytes
with no case folding or Unicode normalization:

| Persisted value | Domain label | Immutable identity |
|---|---|---|
| Conversation title | `conversation.title` | conversation ID |
| Entry content | `entry.content` | lowercase canonical entry UUID |
| Admin checkpoint value | `admin-checkpoint.value` | client ID |
| Episodic memory value | `memory.value` | lowercase canonical memory UUID |

No other current store field uses byte encryption. New encrypted domains must add a distinct
label and migration/test coverage here before use. Moving ciphertext between fields or rows,
or modifying provider/version/nonce header bytes, must fail authentication.

Add `EncryptField(plaintext, domain, identity)` and
`DecryptField(ciphertext, domain, identity)` to `dataencryption.Service`. Preserve the
existing provider interface for external plugin source compatibility and add an optional
AAD-capable provider interface. DEK, KMS, and Vault implement it. Startup fails when database
encryption is enabled but the selected primary provider cannot write v4; `plain` cannot
implement v4. Store call sites must stop swallowing decryption failures or returning raw
ciphertext.

Define shared `EncryptedFieldRecord` and cursor-page types in
`internal/registry/encryptmigration`,
then add optional migration capabilities to both store registries rather than teaching the
command raw datastore schemas:

```go
type EncryptedFieldMigrationStore interface {
    ListEncryptedFields(ctx context.Context, domain, after string, limit int) (EncryptedFieldPage, error)
    CompareAndSwapEncryptedField(ctx context.Context, domain, identity string, oldValue, newValue []byte) (bool, error)
}
```

The core `registry/store` capability exposes `conversation.title`, `entry.content`, and
`admin-checkpoint.value`; the separate `registry/episodic` capability exposes `memory.value`.
Built-in PostgreSQL, SQLite, and MongoDB plugins implement the appropriate capability.
External plugins remain source-compatible, but the migrator validates that every selected
store supports its required capability before mutating anything.

Add resumable `memory-service migrate encryption-fields --to-version=4 --batch-size=500`,
with `--dry-run` to inventory versions without mutation. It scans all four domains in stable
identity order. Each value is decrypted under its existing v1 or explicit legacy-plain
policy, re-encrypted with its v4 context, and conditionally updated only if the ciphertext
still equals the value read. SQL implementations may use a row transaction; MongoDB uses a
single-document conditional update. A failed compare-and-swap is counted as a concurrent
change and left for the next run rather than overwritten. V4 values are skipped, so an
interrupted run resumes safely by rerunning.

An invalid header or authentication failure stops the run immediately with a non-zero exit
after reporting only the domain and immutable record identity; logs never contain ciphertext
or plaintext. Report aggregate counts by domain/version plus compare-and-swap skips. A
successful non-dry run requires zero remaining legacy values and zero corruption errors.

When database encryption is enabled with a real provider, new binaries write v4 immediately;
an explicitly allowed `plain` development configuration remains unencrypted and cannot be a
v4 migration target. Operators stop all replicas, back up data, deploy with v1 reads enabled
and legacy-plain reads only if needed, run the migrator to zero remaining old values, verify
legacy-read metrics stay zero, then disable the flags. Rollback after a v4 write requires
restoring the pre-upgrade backup. No schema reset or mixed deployment is supported.

### Non-root and read-only filesystem contract (F-H1/F-H2)

The release image uses fixed UID/GID `10001:10001`, `USER 10001:10001`, and a read-only
application tree. On Unix, the service sets umask `0077` before creating runtime files. Create
these conventional paths in the image, but require runtime mounts when the root filesystem is
read-only:

| Path | Mode/use | Runtime requirement |
|---|---|---|
| `/app` and `/memory-service` | read-only executable/frontend assets | No writable mount |
| `/var/lib/memory-service/tmp` | `0700`; uploads, DB attachment spooling, response resumption, re-encryption temp files | Ephemeral writable volume; set `MEMORY_SERVICE_TEMP_DIR` |
| `/var/lib/memory-service/attachments` | `0700`; filesystem attachment store | Persistent volume when `attachments-kind=fs`; set `MEMORY_SERVICE_ATTACHMENTS_FS_DIR` |
| `/var/lib/memory-service/data` | `0700`; SQLite database/vector files | Persistent volume for file-backed SQLite configurations |
| `/var/run/memory-service` | `0700`; main/management Unix sockets | Ephemeral writable volume only when sockets are used |

TLS certificates, private keys, and provider credential files are mounted read-only outside
these paths and must be readable by UID 10001; private keys should be `0400` owned by 10001 or
`0440` with GID 10001. The process never chmods/chowns secret mounts. Docker bind mounts must
be pre-owned by 10001; do not add a root entrypoint that repairs ownership.

Kubernetes sets pod `runAsNonRoot`, `runAsUser`, `runAsGroup`, and `fsGroup` to 10001 with
`fsGroupChangePolicy: OnRootMismatch`; the container sets `allowPrivilegeEscalation: false`,
`readOnlyRootFilesystem: true`, drops `ALL` capabilities, and uses
`seccompProfile.type: RuntimeDefault`. Mount an `emptyDir` for the temp path (with a size
limit) and, when needed, the socket path. Filesystem attachments and SQLite require explicit
PVCs; the base manifest must not silently use ephemeral storage for durable data. Disable
service-account token automounting. Startup validates that configured writable directories
exist or can be created, are directories, and are writable by the runtime identity before
starting workers/listeners.

### Management listener and HTTP limits (F-M11/F-M12)

Do not choose a surprise second port. Operators must either explicitly select
`--management-port`/`--management-unix-socket` or explicitly opt into
`--management-on-main-listener`; omission of both fails validation. The management host
defaults to loopback. A non-loopback management TCP bind additionally requires
`--management-allow-non-loopback` /
`MEMORY_SERVICE_MANAGEMENT_ALLOW_NON_LOOPBACK=true`, emits unsafe-config telemetry, and the
operator guide requires a NetworkPolicy/firewall because health/metrics remain unauthenticated
on that listener. Management routers also trust no forwarded proxies.

Set these explicit `http.Server` limits for both plaintext and TLS instances:

| Limit | Main default | Management default | Configuration |
|---|---:|---:|---|
| Header read timeout | 5s | 5s | existing `--read-header-timeout-seconds` applies to both |
| Maximum header bytes | 1 MiB | 64 KiB | `--max-header-bytes`, `--management-max-header-bytes` |
| Keep-alive idle timeout | 120s | 30s | `--idle-timeout`, `--management-idle-timeout` |
| Global read timeout | disabled | disabled | Not configurable; route deadlines below protect bodies |
| Global write timeout | disabled | disabled | Required for gRPC/SSE/recorder streams |

Values below 1 KiB for headers, negative durations, or idle timeouts outside `1s..30m` fail
startup. Apply the equivalent HTTP/2 maximum-header-list setting and gRPC receive-metadata
limit; do not assume `http.Server.MaxHeaderBytes` alone covers HTTP/2.

The existing maximum-body middleware remains the byte limit. Add route-aware body deadlines
of 30 seconds for ordinary REST request bodies, including the small source-URL import JSON
request, and 5 minutes only for multipart attachment upload bodies, configurable through
`--body-read-timeout` and `--attachment-body-read-timeout`. The existing outbound source-fetch
context and HTTP client timeouts continue to govern transfer of the remote object.
Implement them with a per-request timer/context and close only that request body on expiry.
An HTTP/1 implementation may additionally set and clear a socket read deadline, but HTTP/2
must never set a connection-wide deadline that can terminate unrelated multiplexed streams.
Cancel the timer after the body reaches EOF. Requests with no body, gRPC transport bodies,
SSE responses, response-recorder streams, and other accepted long-lived streams do not
receive a global body/write deadline. Slow-body timeout returns `408` with code
`request_timeout` when no response has started; later transport failures close/reset the
stream and log the request ID.

The operator security guide may document only controls that have landed. It must not present
configuration names, defaults, or enforcement behavior as available before their implementing
change ships.

## Testing

- **Encryption unit tests**: reject bit flips, chunk reordering, truncation, wrong final
  marker, data after FINAL, zero/oversized chunks, counter overflow, AAD/header/domain/identity
  mismatch, row/field swaps, and malformed MSEH; prove v1/v2/plain read gating and v4/v3-only
  writes.
- **Encryption migration tests**: dry-run without mutation, resume from a cursor, skip
  v3/plain objects, reject missing/malformed attachment SHA-256, verify the plaintext hash
  before atomic replacement, preserve the old object on hash/replace failure,
  reject a backend without `AtomicAttachmentReplacer` before mutation, clean stale encrypted
  temp files, migrate all four v4 field domains across each datastore, reject concurrent
  compare-and-swap changes, fail safely on corrupt envelopes without logging values, and
  safely retry after interruption.
- **Attachment route tests**: active/unknown types download as attachment with `nosniff` and
  sandbox CSP; every allow-listed type can render inline; SVG/PDF/HTML/mislabeled content,
  omitted disposition, admin routes, token routes, source imports, and S3 overrides cannot
  bypass the policy.
- **Auth unit tests**: `scope=admin`, top-level `roles=admin`, and an unmapped `groups=admin`
  do not grant admin; configured JSON Pointers do; malformed types/limits reject the token;
  wrong/missing audience fails unless the explicit compatibility flag is set.
- **Config unit tests**: known dev DEK, primary `plain`, wildcard credentialed CORS, public
  management fallback, issuer-only OIDC, unintended dual plaintext/TLS, unsafe bind hosts,
  invalid rate syntax, and unwritable runtime paths fail before external initialization.
- **Error tests**: backend failures return a stable public code/request ID without driver/path
  details; request-ID validation, REST headers/bodies, gRPC metadata, recovery, and streaming
  terminal errors follow the shared contract.
- **Rate-limit tests**: verify every route class/key, trusted-proxy interaction, refill and
  burst behavior, bounded idle eviction, per-replica semantics, REST `Retry-After`, gRPC
  `RetryInfo`, management exemptions, and stream admission without duration charging.
- **Frontend tests**: reject `javascript:`/`data:` attachment links and add safe new-window
  relationship flags.
- **Workflow/static tests**: `actionlint`/`zizmor` cover expression-to-shell injection and
  permissions; a shell test verifies Fly deploy does not trace or print secret values.
- **Container policy tests**: built image runs as `10001:10001`; root filesystem is read-only;
  required temp/socket volumes are writable; durable paths are not ephemeral; TLS/key mounts
  remain read-only; rendered Kustomize output has the required security context and no
  automounted service-account token.
- **HTTP/listener tests**: loopback bind defaults, explicit management selection, non-loopback
  acknowledgements, header/idle limits, ordinary/upload body deadlines, and absence of global
  read/write deadlines for gRPC/SSE/recorder streams.
- **Proxy/config tests**: Gin trusts no proxy by default; invalid/universal CIDRs fail;
  untrusted forwarding headers do not change `ClientIP`; production developer UI requires a
  valid origin-only base URL; testing fallback ignores forwarded host/proto; Unix sockets do
  not accept forwarded identity.
- **Site documentation**: build/test the site, verify the Deployment sidebar contains only
  existing routes, check internal links, and ensure Docker/security examples distinguish
  local placeholders from production-required generated secrets and immutable images.
- **Dev/demo tests**: verify Compose ports are loopback-only and generated secrets are
  ignored, rendered demo Kustomize has no public monitoring routes, and the devcontainer has
  no host-home bind, private-key copy, SOCKS listener, or host Docker socket.

Representative acceptance scenarios:

```gherkin
Feature: authenticated and browser-safe attachments

  Scenario: A truncated encrypted attachment is rejected
    Given an attachment encrypted with MSEH stream version 3
    And its final authenticated record has been removed
    When the service reads the attachment to completion
    Then the read fails with an authentication error

  Scenario: Active content cannot be forced inline
    Given an attachment stored with content type "text/html"
    When a client requests it with disposition "inline"
    Then the response disposition is "attachment"
    And the response content type is "application/octet-stream"
    And the response has the "X-Content-Type-Options" value "nosniff"

  Scenario: Forwarded origin cannot alter developer configuration
    Given the developer frontend base URL is "https://memory.example"
    When an untrusted client requests config with forwarded host "attacker.example"
    Then the OIDC redirect URI starts with "https://memory.example/"
```

## Tasks

- [x] F-H1/F-H2: Add non-root container and Kubernetes least-privilege settings, including
  disabling unused service-account token mounting.
- [x] F-H3/F-H4/F-M5/F-M8: Remove usable credential defaults and constrain dev ports.
- [x] F-H5/F-H6: Disable anonymous Grafana and remove public Prometheus/Grafana routes.
- [x] F-H7/F-H9: Remove the devcontainer host-home/private-key copy and microsocks/port 1080;
  use SSH-agent forwarding and narrow non-secret configuration.
- [x] F-H8: Retain isolated DinD, pin its feature release, prohibit a host Docker socket, and
  document the accepted trusted-workspace development risk.
- [x] F-H10: Pin runtime/deployment images immutably. The repository Dockerfile stages,
  Kustomize manifests, demo deployment images, and pulled Compose images are digest-pinned;
  Compose services built locally use non-`latest` local tags instead of pullable mutable tags.
- [x] F-H11: Make plaintext provider/fallback explicit, migration-only, and fail-closed.
- [ ] F-H11/F-M13: Implement MSEH v4 field AAD and the resumable four-domain migration.
- [ ] F-H12: Implement MSEH v3 64-KiB AES-GCM records, v2 read telemetry/gating, and the
  resumable attachment migrator.
- [ ] F-H12: Implement optional `AtomicAttachmentReplacer` support for built-in stores and
  fail migration safely for plugins that do not provide it.
- [x] F-H13/F-M9: Enforce attachment MIME/disposition/header/isolation policy.
- [x] F-H14/F-M2: Implement JSON Pointer role claims, limits, and required audience policy.
- [x] F-H15: Remove GitHub expression interpolation from release shell source.
- [x] F-H16: Stop tracing/printing Fly secrets and remove the obsolete signing secret.
- [x] F-M1/F-L4/F-M14: Reject unsafe startup combinations, including exact CORS-origin
  validation, OIDC TLS-skip rejection, aggregate startup error reporting, and service-owned
  known demo-secret validation.
- [ ] F-M3: Centralize the stable REST/gRPC error and request-ID contract.
- [ ] F-M4: Add the five-class process-local token-bucket policy and telemetry.
- [ ] F-M6/F-M7/F-L8: Pin actions, minimize token permissions, and repair path filters.
  Actions are commit-pinned and CI path filters no longer skip workflow/devcontainer changes;
  release, Pages, and snapshot jobs use narrower permissions. The combined CI matrix still
  needs splitting before token permissions can be minimized per job.
- [x] F-M10: Add trust-none proxy initialization, explicit CIDR parsing, production base-URL
  validation, and forwarded-origin removal.
- [x] F-M11/F-M12/F-M16: Add explicit bind hosts, route-aware HTTP limits, management
  selection/isolation, and plaintext/TLS deployment policy. TCP bind hosts now default to
  loopback, container deployments set explicit `0.0.0.0`, and header/idle listener limits are
  configured; management selection now requires a dedicated listener or explicit main-listener
  opt-in outside testing; dual plaintext/TLS TCP listeners are rejected outside testing; and
  non-loopback management binds require an explicit deployment-boundary acknowledgement.
  Route-aware body read deadlines are configured, and non-loopback plaintext API binds require
  an explicit deployment-boundary acknowledgement.
- [x] F-M15/F-L5: Correct active encryption and attachment-signing documentation.
- [x] F-M17: Validate frontend attachment URL schemes.
- [x] F-L1/F-L2/F-L6/F-L7: Tighten remaining local/deployment hygiene.
- [ ] Document the coordinated stop/backup/upgrade/migrate rollout and forward-only rollback
  boundary in release notes and operator docs. Release notes and the security guide now
  document the current startup-breaking changes plus the forward-only MSEH rollback boundary;
  command-specific migration docs remain blocked on the v3/v4 migrators.
- [x] Add `site/src/pages/docs/deployment/security.mdx` with the production security checklist.
- [x] Re-enable the Deployment sidebar with only Docker and Security Hardening.
- [x] Refresh the Docker deployment page, configuration reference, attachment documentation,
  and FAQ links/semantics alongside the implementing changes.

## Files to Modify

| Area | Likely files |
|---|---|
| Container/Kubernetes | `main.go` or a Unix runtime-initialization helper, `Dockerfile`, `deploy/kustomize/base/deployment.yaml`, service-account manifests, image overlays |
| Encryption/config | `contracts/protobuf/dataencryption/v1/encryption_header.proto`, `internal/config/**`, `internal/dataencryption/**`, `internal/registry/encrypt/**`, `internal/plugin/encrypt/**`, encrypted store and attachment call sites |
| Encryption migrations | `internal/cmd/migrate/**`, PostgreSQL/SQLite/Mongo field iterators, encrypted attachment wrapper, optional `AtomicAttachmentReplacer` implementations, temp-file cleanup |
| Attachments | `internal/plugin/route/attachments/attachments.go`, `internal/registry/attach/plugin.go`, S3 signing options, generated/wrapper attachment routes |
| Authentication | `internal/security/auth.go` and its tests |
| API errors/request IDs | `contracts/openapi/openapi.yml`, `contracts/openapi/openapi-admin.yml`, generated clients/servers, shared REST/gRPC error helpers |
| HTTP/runtime | `internal/cmd/serve/**`, rate-limit middleware, `internal/plugin/route/developer/developer.go`, developer tests |
| Frontends | developer `HistoryRenderer.tsx`, chat frontend conversation attachment rendering |
| CI/release | `.github/workflows/**`, `deploy/fly/deploy.sh` |
| Dev/demo | `compose.yaml`, Keycloak realm, monitoring routes, `.devcontainer/**` |
| Engineering documentation | `docs/encryption.md`, `deploy/fly/README.md`, release notes |
| Operator security guide | `site/src/pages/docs/deployment/security.mdx`, `site/src/components/DocsSidebar.astro` |
| Existing site guidance | `site/src/pages/docs/deployment/docker.mdx`, `site/src/pages/docs/configuration.mdx`, `site/src/pages/docs/concepts/attachments.mdx`, `site/src/pages/docs/faq.mdx`, `site/FACTS.md` |
| Context exclusions | `.dockerignore` |

## Verification

```bash
# Application changes
go build ./...
go test ./internal/security/... ./internal/config/... \
  ./internal/dataencryption/... ./internal/plugin/encrypt/... \
  ./internal/plugin/attach/... ./internal/plugin/route/attachments/... \
  ./internal/plugin/store/... \
  ./internal/plugin/route/developer/... ./internal/cmd/migrate/... \
  ./internal/cmd/serve/... > test.log 2>&1

# Frontend changes
(cd frontends/chat-frontend && npm run lint && npm run build)
(cd frontends/developer && npm run lint && npm run build)

# Workflow and manifest policy
actionlint .github/workflows/*.yml
kustomize build deploy/kustomize/base > rendered.yaml

# Runtime identity
docker build -t memory-service:hardened .
test "$(docker inspect --format '{{.Config.User}}' memory-service:hardened)" = "10001:10001"
docker run --rm --read-only --entrypoint id memory-service:hardened
docker run --rm --read-only \
  --tmpfs /var/lib/memory-service/tmp:uid=10001,gid=10001,mode=0700 \
  --entrypoint sh memory-service:hardened \
  -c 'test -w /var/lib/memory-service/tmp && test ! -w /app'

# Site documentation (run separately from concurrent Java builds)
task test:site > site-test.log 2>&1
```

Inspect both `test.log` and `site-test.log` for all failures rather than truncating test
output.

## Non-Goals

- Dynamic penetration testing or cloud-tenant policy review.
- Resolving dependency CVEs; dependency maintenance has its own workflow.
- Redesigning conversation/group authorization or multi-tenancy.
- Turning demo applications into production identity examples.
- Supporting multiple simultaneous OIDC issuers or issuer-specific role mappings.
- Completing the separate focused Java client/integration audit described by F-J1.
- Providing a distributed rate-limit counter; ingress remains responsible for cluster-wide limits.
- Supporting rolling or mixed-version memory-service deployments during the MSEH v3/v4
  upgrade.

## Implementation Readiness

**Ready.** The attachment stream and field encryption formats, coordinated non-mixed
migrations, attachment isolation, OIDC claims/audience policy, public error contract,
per-replica rate limits, startup validation, proxy/listener policy, container filesystem,
HTTP limits, operator documentation, compatibility boundaries, and acceptance tests are
specified. Implement this umbrella as the independent workstreams above rather than one pull
request; a workstream may refine internal structure but must not change these public,
persistence, or deployment contracts without updating this enhancement first.
