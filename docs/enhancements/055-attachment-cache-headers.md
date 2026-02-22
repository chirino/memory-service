---
status: implemented
---

# Enhancement 055: Attachment Download Cache Headers

> **Status**: Implemented.

## Summary

Add proper HTTP cache headers (`ETag`, `Cache-Control`, `Vary`) to attachment download responses so that clients and intermediate proxies can cache attachment content efficiently without leaking content across users. Leverage the existing `sha256` field on attachments as the `ETag` value.

## Motivation

Attachments are immutable once uploaded — the content behind a given attachment ID never changes. Despite this, the current implementation uses `Cache-Control: private, no-store` for direct-stream downloads, preventing any client-side caching. Every view of an image thumbnail, PDF preview, or audio clip triggers a full re-download from the server.

This is especially wasteful for:
- **Chat UIs** that re-render message history with inline images on every page load.
- **Mobile clients** on metered connections re-downloading the same attachments.
- **Multi-tab/window scenarios** where the same attachment is fetched independently.

Proper caching would allow browsers to serve repeated requests from cache while still respecting user-level access control boundaries.

### Current Cache Headers

| Endpoint | Scenario | Current Header |
|----------|----------|----------------|
| `GET /v1/attachments/{id}` | S3 redirect | `private, max-age=3600` |
| `GET /v1/attachments/{id}` | Direct stream | `private, no-store` |
| `GET /v1/attachments/{id}` | sourceUrl redirect | `no-cache` |
| `GET /v1/attachments/download/{token}/{filename}` | Signed token | `private, max-age={remaining}` |
| `GET /v1/admin/attachments/{id}/content` | S3 redirect | `private, max-age=3600` |
| `GET /v1/admin/attachments/{id}/content` | Direct stream | `private, no-store` |

**Problems:**
1. **No `ETag` anywhere** — conditional GET (`If-None-Match`) is not supported, so even a re-validation requires streaming full content.
2. **`no-store` on direct streams** — browser cannot cache at all, even for the same session.
3. **No `Vary` header** — proxies that ignore `private` could serve cached content to the wrong user.

## Design

### Guiding Principles

1. **`private` always** — attachment content is user-scoped. Shared/CDN caches must never store it. Every response includes `Cache-Control: private`.
2. **`ETag` from sha256** — the sha256 hex digest already computed at upload time is a perfect strong ETag. It's content-derived, stable, and already stored in the attachment record.
3. **Conditional GET support** — check `If-None-Match` request header; return `304 Not Modified` when the ETag matches, avoiding the full response body.
4. **Immutable hint** — once an attachment reaches `status=ready`, its content never changes. We can use `immutable` to tell modern browsers not to re-validate during `max-age`. Combined with `private`, this prevents both proxy caching and unnecessary revalidation.

### Target Cache Headers

| Endpoint | Scenario | Cache-Control | ETag | 304 Support |
|----------|----------|---------------|------|-------------|
| `GET /v1/attachments/{id}` | Direct stream | `private, max-age=86400, immutable` | `"<sha256>"` | Yes |
| `GET /v1/attachments/{id}` | S3 redirect | `private, max-age=3600` | `"<sha256>"` | Yes |
| `GET /v1/attachments/{id}` | sourceUrl redirect | `no-cache` (unchanged) | — | No |
| `GET /v1/attachments/download/{token}/{filename}` | Signed token | `private, max-age={remaining}, immutable` | `"<sha256>"` | Yes |
| `GET /v1/admin/attachments/{id}/content` | Direct stream | `private, max-age=86400, immutable` | `"<sha256>"` | Yes |
| `GET /v1/admin/attachments/{id}/content` | S3 redirect | `private, max-age=3600` | `"<sha256>"` | Yes |

Notes:
- `private` prevents shared proxies from caching. Only the user's browser cache is used.
- `immutable` tells the browser not to re-validate the cached response within `max-age`. This is safe because attachment content never changes.
- `max-age=86400` (24 hours) for direct streams — generous because content is immutable; `immutable` prevents revalidation within this window.
- S3 redirects keep `max-age=3600` because the pre-signed URL itself expires (typically 1 hour).
- ETag is quoted per HTTP spec: `ETag: "<sha256hex>"`.
- `Vary: Authorization` is not needed because `Cache-Control: private` already ensures the browser only serves from its own cache.

### Conditional GET (304 Not Modified)

When the client sends `If-None-Match: "<sha256>"`, the server can return `304 Not Modified` without streaming the body. This is most valuable for:

1. **Direct stream endpoints** — avoids reading from DB/S3 entirely.
2. **Signed token endpoint** — client re-visits a token URL; content hasn't changed.

For S3 redirect responses (302), the 304 optimization is less critical because the redirect itself is cheap, but we include the ETag for consistency so that clients caching the final resource can validate.

### Implementation

#### Helper Method

Add a shared helper (e.g., on a new `AttachmentResponseHelper` utility or as a `default` method) to build cache-aware responses, avoiding duplication across the three resource classes:

```java
public class AttachmentResponseHelper {

    /**
     * Check If-None-Match against the attachment's sha256.
     * Returns an Optional 304 response if the ETag matches.
     */
    public static Optional<Response> checkNotModified(
            String ifNoneMatch, AttachmentDto att, String cacheControl) {
        if (att.sha256() != null && ifNoneMatch != null) {
            String etag = "\"" + att.sha256() + "\"";
            // Handle both quoted and unquoted, plus weak ETags
            if (ifNoneMatch.equals(etag)
                    || ifNoneMatch.equals(att.sha256())
                    || ifNoneMatch.contains(etag)) {
                return Optional.of(
                        Response.notModified()
                                .header("ETag", etag)
                                .header("Cache-Control", cacheControl)
                                .build());
            }
        }
        return Optional.empty();
    }

    /**
     * Add ETag and Cache-Control to a response builder.
     */
    public static void addCacheHeaders(
            Response.ResponseBuilder builder, AttachmentDto att, String cacheControl) {
        builder.header("Cache-Control", cacheControl);
        if (att.sha256() != null) {
            builder.header("ETag", "\"" + att.sha256() + "\"");
        }
    }
}
```

#### `AttachmentsResource.java` — `retrieve()`

Inject `@HeaderParam("If-None-Match")` and add ETag/cache logic:

```java
@GET
@Path("/{id}")
public Response retrieve(
        @PathParam("id") String id,
        @HeaderParam("If-None-Match") String ifNoneMatch) {
    // ... existing lookup and access control ...

    // After storageKey null checks, before streaming:
    String cacheControl = "private, max-age=86400, immutable";

    // Check conditional GET
    Optional<Response> notModified =
            AttachmentResponseHelper.checkNotModified(ifNoneMatch, att, cacheControl);
    if (notModified.isPresent()) {
        return notModified.get();
    }

    // S3 redirect path
    if (signedUrl.isPresent()) {
        Response.ResponseBuilder builder = Response.temporaryRedirect(signedUrl.get());
        AttachmentResponseHelper.addCacheHeaders(
                builder, att, "private, max-age=" + signedUrlExpiry.toSeconds());
        return builder.build();
    }

    // Direct stream path
    Response.ResponseBuilder builder = Response.ok(stream)
            .header("Content-Type", att.contentType());
    // ... Content-Length, Content-Disposition ...
    AttachmentResponseHelper.addCacheHeaders(builder, att, cacheControl);
    return builder.build();
}
```

#### `AttachmentDownloadResource.java` — `download()`

Same pattern — inject `If-None-Match`, add ETag, check 304:

```java
@GET
@Path("/{token}/{filename}")
public Response download(
        @PathParam("token") String token,
        @PathParam("filename") String filename,
        @HeaderParam("If-None-Match") String ifNoneMatch) {
    // ... existing token verification and lookup ...

    long cacheMaxAge =
            Math.max(0, expiresAt.getEpochSecond() - Instant.now().getEpochSecond());
    String cacheControl = "private, max-age=" + cacheMaxAge + ", immutable";

    // Check conditional GET
    Optional<Response> notModified =
            AttachmentResponseHelper.checkNotModified(ifNoneMatch, att, cacheControl);
    if (notModified.isPresent()) {
        return notModified.get();
    }

    // Stream with ETag
    Response.ResponseBuilder builder = Response.ok(stream)
            .header("Content-Type", att.contentType());
    // ... Content-Length, Content-Disposition ...
    AttachmentResponseHelper.addCacheHeaders(builder, att, cacheControl);
    return builder.build();
}
```

#### `AdminAttachmentsResource.java` — `getAttachmentContent()`

Same pattern as the user endpoint.

### Security Considerations

| Concern | Mitigation |
|---------|------------|
| **Cross-user cache leakage** | `Cache-Control: private` ensures only the browser's own cache is used; shared proxies must not store the response. |
| **ETag as information disclosure** | The sha256 is a hash of content the user already has access to — it reveals nothing beyond what downloading the file would. It is not enumerable (requires knowing an attachment ID + having access). |
| **Signed token endpoints are unauthenticated** | The HMAC token itself is the authorization. `private` still applies to prevent proxy caching. The `max-age` is bounded by token expiry, so cached content expires when the token does. |
| **CDN/reverse-proxy stripping `private`** | Out of scope — deployment-level concern. Document that CDN rules must not override `Cache-Control` for attachment paths. |
| **Browser shared cache (e.g., disk cache)** | `private` excludes shared caches per HTTP spec. The browser's private disk cache is per-user by OS-level isolation. |

### What About `Vary: Authorization`?

`Vary: Authorization` is sometimes recommended to prevent proxy caches from serving one user's cached response to another. However, since we already use `Cache-Control: private`, shared caches must not store the response at all, making `Vary` redundant for this purpose. Adding it would not hurt but provides no additional safety.

For the unauthenticated signed-token endpoint, `Authorization` isn't even present in the request, so `Vary: Authorization` would be meaningless there. The token-expiry-bounded `max-age` is sufficient.

## Tasks

- [x] Create `AttachmentResponseHelper` utility class in `io.github.chirino.memory.api`
- [x] Update `AttachmentsResource.retrieve()` — add `If-None-Match` param, ETag header, 304 support, update `Cache-Control` from `no-store` to `max-age=86400, immutable`
- [x] Update `AttachmentDownloadResource.download()` — add `If-None-Match` param, ETag header, 304 support, add `immutable` directive
- [x] Update `AdminAttachmentsResource.getAttachmentContent()` — same changes as user endpoint
- [x] Add new Cucumber step definitions for header testing (see below)
- [x] Add Cucumber tests for cache headers on user attachment downloads
- [x] Add Cucumber tests for cache headers on admin attachment downloads
- [x] Add Cucumber tests for cache headers on signed-token downloads
- [x] Add Cucumber tests for conditional GET (304 Not Modified)

## Cucumber Tests

### New Step Definitions Required

The existing step `the response header {string} should contain {string}` handles substring matching. We also need:

- **`I call GET {string} expecting binary with header {string} = {string}`** — sends a GET request with a custom request header (needed for `If-None-Match`).
- **`the response header {string} should not be present`** — asserts a header is absent (useful for verifying no `ETag` on error/redirect responses).

```java
@io.cucumber.java.en.When("I call GET {string} expecting binary with header {string} = {string}")
public void iCallGETExpectingBinaryWithHeader(String path, String headerName, String headerValue) {
    trackUsage();
    String renderedPath = renderTemplate(path);
    var requestSpec = given();
    requestSpec = authenticateRequest(requestSpec);
    requestSpec = requestSpec.header(headerName, renderTemplate(headerValue));
    this.lastResponse = requestSpec.when().get(renderedPath);
}

@io.cucumber.java.en.Then("the response header {string} should not be present")
public void theResponseHeaderShouldNotBePresent(String headerName) {
    trackUsage();
    String actual = lastResponse.getHeader(headerName);
    assertThat("Response header " + headerName + " should not be present", actual, is(nullValue()));
}
```

### Test Scenarios — `attachments-rest.feature`

Add to the existing feature file:

```gherkin
  # --- Cache Headers ---

  Scenario: Attachment download includes ETag and Cache-Control headers
    When I upload a file "cached.txt" with content type "text/plain" and content "Cache Me"
    Then the response status should be 201
    And set "cachedAttId" to the json response field "id"
    And set "cachedSha256" to the json response field "sha256"
    When I call GET "/v1/attachments/${cachedAttId}" expecting binary
    Then the response status should be 200
    And the response header "ETag" should contain "${cachedSha256}"
    And the response header "Cache-Control" should contain "private"
    And the response header "Cache-Control" should contain "max-age="
    And the response header "Cache-Control" should contain "immutable"

  Scenario: Attachment download returns 304 Not Modified for matching ETag
    When I upload a file "etag-test.txt" with content type "text/plain" and content "ETag Content"
    Then the response status should be 201
    And set "etagAttId" to the json response field "id"
    And set "etagSha256" to the json response field "sha256"
    When I call GET "/v1/attachments/${etagAttId}" expecting binary with header "If-None-Match" = "\"${etagSha256}\""
    Then the response status should be 304
    And the response header "ETag" should contain "${etagSha256}"
    And the response header "Cache-Control" should contain "private"

  Scenario: Attachment download returns full response for non-matching ETag
    When I upload a file "etag-miss.txt" with content type "text/plain" and content "Full Response"
    Then the response status should be 201
    And set "etagMissAttId" to the json response field "id"
    When I call GET "/v1/attachments/${etagMissAttId}" expecting binary with header "If-None-Match" = "\"0000000000000000000000000000000000000000000000000000000000000000\""
    Then the response status should be 200
    And the binary response content should be "Full Response"
    And the response header "ETag" should contain "etagMiss"

  Scenario: Cache-Control is private and never public on attachment downloads
    When I upload a file "private.txt" with content type "text/plain" and content "Private Content"
    Then the response status should be 201
    And set "privateAttId" to the json response field "id"
    When I call GET "/v1/attachments/${privateAttId}" expecting binary
    Then the response status should be 200
    And the response header "Cache-Control" should contain "private"
```

### Test Scenarios — `admin-attachments-rest.feature`

Add to the existing feature file:

```gherkin
  # --- Cache Headers ---

  Scenario: Admin attachment content download includes ETag and Cache-Control
    Given I am authenticated as user "bob"
    And I have a conversation with title "Admin Cache Conv"
    When I upload a file "admin-cached.txt" with content type "text/plain" and content "Admin Cache Content"
    Then the response status should be 201
    And set "adminCachedId" to the json response field "id"
    And set "adminCachedSha" to the json response field "sha256"
    Given I am authenticated as admin user "alice"
    When I call GET "/v1/admin/attachments/${adminCachedId}/content" expecting binary
    Then the response status should be 200
    And the response header "ETag" should contain "${adminCachedSha}"
    And the response header "Cache-Control" should contain "private"
    And the response header "Cache-Control" should contain "max-age="
    And the response header "Cache-Control" should contain "immutable"

  Scenario: Admin attachment content returns 304 for matching ETag
    Given I am authenticated as user "bob"
    And I have a conversation with title "Admin ETag Conv"
    When I upload a file "admin-etag.txt" with content type "text/plain" and content "Admin ETag Content"
    Then the response status should be 201
    And set "adminEtagId" to the json response field "id"
    And set "adminEtagSha" to the json response field "sha256"
    Given I am authenticated as admin user "alice"
    When I call GET "/v1/admin/attachments/${adminEtagId}/content" expecting binary with header "If-None-Match" = "\"${adminEtagSha}\""
    Then the response status should be 304
    And the response header "ETag" should contain "${adminEtagSha}"
```

### Test Scenarios — Signed Token Download

Add a new scenario to `attachments-rest.feature` that exercises the token download path:

```gherkin
  Scenario: Signed token download includes ETag and Cache-Control headers
    When I upload a file "token-cached.txt" with content type "text/plain" and content "Token Cache Content"
    Then the response status should be 201
    And set "tokenCachedId" to the json response field "id"
    And set "tokenCachedSha" to the json response field "sha256"
    When I call GET "/v1/attachments/${tokenCachedId}/download-url"
    Then the response status should be 200
    And set "tokenUrl" to the json response field "url"
    When I call GET "${tokenUrl}" expecting binary without authentication
    Then the response status should be 200
    And the response header "ETag" should contain "${tokenCachedSha}"
    And the response header "Cache-Control" should contain "private"
    And the response header "Cache-Control" should contain "max-age="

  Scenario: Signed token download returns 304 for matching ETag
    When I upload a file "token-etag.txt" with content type "text/plain" and content "Token ETag Content"
    Then the response status should be 201
    And set "tokenEtagId" to the json response field "id"
    And set "tokenEtagSha" to the json response field "sha256"
    When I call GET "/v1/attachments/${tokenEtagId}/download-url"
    Then the response status should be 200
    And set "tokenEtagUrl" to the json response field "url"
    When I call GET "${tokenEtagUrl}" expecting binary without authentication with header "If-None-Match" = "\"${tokenEtagSha}\""
    Then the response status should be 304
    And the response header "ETag" should contain "${tokenEtagSha}"
```

Note: The signed-token scenarios need an additional step for unauthenticated GET with custom headers:

```java
@io.cucumber.java.en.When("I call GET {string} expecting binary without authentication")
public void iCallGETExpectingBinaryWithoutAuth(String path) {
    trackUsage();
    String renderedPath = renderTemplate(path);
    this.lastResponse = given().when().get(renderedPath);
}

@io.cucumber.java.en.When("I call GET {string} expecting binary without authentication with header {string} = {string}")
public void iCallGETExpectingBinaryWithoutAuthWithHeader(String path, String headerName, String headerValue) {
    trackUsage();
    String renderedPath = renderTemplate(path);
    this.lastResponse = given().header(headerName, renderTemplate(headerValue)).when().get(renderedPath);
}
```

## Verification

1. `./mvnw compile` — verify compilation
2. `./mvnw test -pl memory-service > test.log 2>&1` — all tests pass including new cache header scenarios
3. Manual: upload attachment, `GET /v1/attachments/{id}` and verify `ETag` and `Cache-Control` headers
4. Manual: repeat request with `If-None-Match: "<sha256>"` and verify `304 Not Modified`
5. Manual: verify `Cache-Control` always includes `private` (never `public`)
