# Enhancement 050: S3 Proxy Download Mode

## Summary

Add a configuration property `memory-service.attachments.s3.direct-download` (default `true`) that controls whether S3-backed attachment downloads return S3 pre-signed URLs directly to the client or proxy the download through the memory service using HMAC-signed token URLs (the same mechanism used by DB storage).

## Motivation

Many deployments use S3-compatible object stores (MinIO, Ceph, LocalStack, OpenShift Data Foundation, etc.) on internal networks that are **not reachable from the end user's browser**. The server can reach the S3 endpoint, but clients cannot. In this scenario, returning a pre-signed S3 URL is useless — the browser cannot resolve or connect to the S3 host.

Today the system has two download behaviors based on the storage backend:

| Backend | `GET .../download-url` returns | `GET .../content` (direct) |
|---------|-------------------------------|---------------------------|
| **DB** | HMAC-signed token URL → proxied through memory service | Streams bytes directly |
| **S3** | S3 pre-signed URL → client downloads from S3 | 302 redirect to S3 pre-signed URL |

The S3 behavior assumes the S3 endpoint is publicly reachable. When it isn't, both the pre-signed URL and the 302 redirect fail in the browser.

## Design

### Configuration

A single boolean property controls the behavior:

| Property | Values | Default | Description |
|----------|--------|---------|-------------|
| `memory-service.attachments.s3.direct-download` | `true`, `false` | `true` | When `true` (default), S3 pre-signed URLs are returned directly to clients. When `false`, downloads are proxied through the memory service using HMAC-signed token URLs. |

When `direct-download=false`, S3-backed attachments behave identically to DB-backed attachments from the client's perspective — all downloads flow through the memory service.

### Behavior Matrix

| `direct-download` | `GET .../download-url` | `GET .../content` |
|-------------------|------------------------|-------------------|
| `true` (default) | S3 pre-signed URL | 302 redirect to S3 pre-signed URL |
| `false` | HMAC token URL → memory service streams from S3 | Memory service streams from S3 directly |

### Implementation

The change is scoped entirely to `S3FileStore.getSignedUrl()`. When `direct-download=false`, it returns `Optional.empty()` instead of generating a pre-signed URL. All existing call sites already handle the `Optional.empty()` case — they fall through to the HMAC-signed token path (for `download-url` endpoints) or stream bytes directly (for `content` endpoints).

#### Affected Call Sites (no changes needed)

All four call sites use the pattern `if (signedUrl.isPresent()) { ... } else { ... }` and already have correct fallback behavior:

1. **`AttachmentsResource.getContent()`** (line 273) — Falls through to stream bytes directly via `fileStore().retrieve()`
2. **`AttachmentsResource.getDownloadUrl()`** (line 369) — Falls through to HMAC token generation via `downloadUrlSigner.createToken()`
3. **`AdminAttachmentsResource.getContent()`** (line 182) — Falls through to stream bytes directly
4. **`AdminAttachmentsResource.getDownloadUrl()`** (line 237) — Falls through to HMAC token generation

#### File Changes

**`AttachmentConfig.java`** — Add the new config property:

```java
@ConfigProperty(name = "memory-service.attachments.s3.direct-download", defaultValue = "true")
boolean s3DirectDownload;

public boolean isS3DirectDownload() {
    return s3DirectDownload;
}
```

**`S3FileStore.java`** — Check the config flag before generating a pre-signed URL:

```java
@Override
public Optional<URI> getSignedUrl(String storageKey, Duration expiry) {
    if (!config.isS3DirectDownload()) {
        return Optional.empty();
    }
    // ... existing pre-signed URL generation ...
}
```

**`application.properties`** — Add default (for documentation):

```properties
memory-service.attachments.s3.direct-download=true
```

### Image Generation (`sourceUrl`) Interaction

Enhancement 047 introduced `sourceUrl`-based attachments where the `download-url` endpoint returns a signed URL to the original `sourceUrl` while the attachment is still downloading. When `direct-download=false`:

- **`status=downloading`**: The `sourceUrl` pass-through is independent of S3 — it's handled before the `getSignedUrl()` call. No change needed.
- **`status=ready`**: The stored file path flows through `getSignedUrl()` as normal, which now returns `Optional.empty()`, causing the HMAC token fallback. Correct behavior.

## Configuration Documentation Update

Add to the S3 Storage section of `site/src/pages/docs/configuration.md`:

```markdown
| `memory-service.attachments.s3.direct-download` | `true`, `false` | `true` | When `true`, download URLs point directly at S3 (pre-signed). When `false`, downloads are proxied through the memory service — use this when S3 is on an internal network not reachable by browsers. |
```

## Tasks

- [ ] Add `s3DirectDownload` config property to `AttachmentConfig.java`
- [ ] Update `S3FileStore.getSignedUrl()` to check config flag
- [ ] Add default to `application.properties`
- [ ] Update `site/src/pages/docs/configuration.md` with new property
- [ ] Add Cucumber test scenario verifying proxy behavior when `direct-download=false`
- [ ] Update `docs/enhancements/037-attachments.md` Phase 4 notes to reference this enhancement

## Verification

1. `./mvnw compile` — verify compilation
2. `./mvnw test -pl memory-service > test.log 2>&1` — existing tests still pass (default `true` preserves current behavior)
3. Manual: set `MEMORY_SERVICE_ATTACHMENTS_S3_DIRECT_DOWNLOAD=false`, upload attachment with S3 store, verify `download-url` returns an HMAC token URL instead of an S3 pre-signed URL
