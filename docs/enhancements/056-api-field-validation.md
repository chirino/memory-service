---
status: proposed
---

# Enhancement 056: API Field Validation & Maximum Lengths

> **Status**: Proposed.

## Summary

Add input validation to all API request fields with explicit maximum lengths, pattern constraints, and required-field enforcement. This hardens the service against malformed requests and denial-of-service via oversized payloads, and produces clear error responses for invalid input.

## Motivation

Currently the API has **no server-side validation** on request DTOs:

1. **No `@Size`, `@NotNull`, `@Pattern`, or `@Valid` annotations** exist on any DTO class in `memory-service/src/main/java/.../api/dto/`.
2. **No `maxLength` or `pattern` constraints** are declared in the OpenAPI spec (`openapi.yml`, `openapi-admin.yml`).
3. **Database columns use unbounded types**: `TEXT` for `user_id`, `client_id`, `owner_user_id`, `content_type`, `channel`, `access_level`; `BYTEA` for `title` and `content`; `JSONB` for `metadata`.
4. Only `attachments` columns have length limits: `VARCHAR(255)` for `storage_key` and `filename`, `VARCHAR(127)` for `content_type`, `VARCHAR(64)` for `sha256`.
5. The only request size limit is the HTTP body size (`quarkus.http.limits.max-body-size`, auto-derived as 2x `memory-service.attachments.max-size` = 20MB default), which is far too generous for non-attachment endpoints.

This means a caller can submit a conversation title that is megabytes long, a `userId` with arbitrary characters, or a metadata map with thousands of keys — the service will happily store it all.

### Risks Addressed

- **Denial of service**: Oversized string fields consume memory, database storage, and index space.
- **Data integrity**: No enforcement of expected formats (e.g., `contentType` should look like a MIME type).
- **Poor developer experience**: Invalid requests produce cryptic database errors or silent data corruption instead of clear 400 responses.

## Design

### Validation Strategy

1. **Jakarta Bean Validation** (`jakarta.validation`) annotations on DTO classes.
2. **`@Valid` on JAX-RS resource method parameters** to trigger automatic validation.
3. **Quarkus validation error mapper** (built-in) returns structured 400 responses.
4. **OpenAPI spec** updated with matching `maxLength`, `minLength`, `pattern` constraints for client-side awareness.

### Proposed Field Limits

#### Conversations

| Field | Type | Constraint | Rationale |
|-------|------|------------|-----------|
| `title` | String | `@Size(max = 500)`, nullable | Titles are displayed in UI lists; 500 chars is generous |
| `metadata` keys | String | `@Size(max = 100)` per key | Metadata keys should be short identifiers |
| `metadata` values | Object | Total serialized size `<= 16KB` | Prevents unbounded JSONB growth |
| `metadata` map | Map | `@Size(max = 50)` entries | Caps the number of metadata keys |

#### Entries

| Field | Type | Constraint | Rationale |
|-------|------|------------|-----------|
| `contentType` | String | `@NotBlank`, `@Size(max = 127)`, `@Pattern(regexp = "^[a-zA-Z0-9][a-zA-Z0-9!#$&\\-^_.+]*(/[a-zA-Z0-9][a-zA-Z0-9!#$&\\-^_.+]*)?$")` | Must be a valid MIME-like type; matches `VARCHAR(127)` from attachments |
| `content` | List | `@NotNull`, `@Size(max = 1000)` | Content elements list; bounded to prevent unbounded arrays |
| `userId` | String | `@Size(max = 255)`, nullable | OAuth subject IDs are typically short; 255 is safe |
| `clientId` | String | `@Size(max = 255)`, nullable | Agent client identifiers |
| `indexedContent` | String | `@Size(max = 100_000)`, nullable | Full-text search content; 100K chars covers large documents |
| `channel` | Enum | Already constrained by enum deserialization | `history` or `memory` only |

#### Search

| Field | Type | Constraint | Rationale |
|-------|------|------------|-----------|
| `query` | String | `@NotBlank`, `@Size(min = 1, max = 1000)` | Search queries don't need to be enormous |
| `limit` | Integer | `@Min(1)`, `@Max(200)` | Prevent unbounded result sets |

#### Sharing

| Field | Type | Constraint | Rationale |
|-------|------|------------|-----------|
| `userId` | String | `@NotBlank`, `@Size(max = 255)` | Target user for sharing |
| `accessLevel` | Enum | Already constrained by enum | `owner`, `manager`, `writer`, `reader` |

#### Ownership Transfers

| Field | Type | Constraint | Rationale |
|-------|------|------------|-----------|
| `conversationId` | UUID | `@NotNull` | Required target conversation |
| `newOwnerUserId` | String | `@NotBlank`, `@Size(max = 255)` | Target user |

#### Pagination (all endpoints)

| Field | Type | Constraint | Rationale |
|-------|------|------------|-----------|
| `limit` | Integer | `@Min(1)`, `@Max(200)` | Prevent unbounded queries (admin endpoints may allow higher) |
| `after` | String | `@Size(max = 100)`, nullable | Cursor tokens are UUIDs or timestamps |

#### Index Entries

| Field | Type | Constraint | Rationale |
|-------|------|------------|-----------|
| `indexedContent` | String | `@NotBlank`, `@Size(max = 100_000)` | Same as entry indexedContent |

### Error Response Format

Quarkus built-in `ResteasyReactiveViolationExceptionMapper` returns:

```json
{
  "title": "Constraint Violation",
  "status": 400,
  "violations": [
    {
      "field": "createEntry.request.contentType",
      "message": "must not be blank"
    },
    {
      "field": "createEntry.request.content",
      "message": "size must be between 1 and 1000"
    }
  ]
}
```

To align with the project's `ErrorResponse` format, add a custom `ConstraintViolationExceptionMapper`:

```java
@ServerExceptionMapper
public Response handleConstraintViolation(ConstraintViolationException e) {
    List<Map<String, String>> violations = e.getConstraintViolations().stream()
            .map(v -> Map.of(
                    "field", extractFieldName(v.getPropertyPath()),
                    "message", v.getMessage()))
            .toList();

    ErrorResponse error = new ErrorResponse();
    error.setError("Validation failed");
    error.setCode("validation_error");
    error.setDetails(Map.of("violations", violations));
    return Response.status(400)
            .type(MediaType.APPLICATION_JSON)
            .entity(error)
            .build();
}
```

### Code Changes

#### 1. Add Validation Dependency

The `quarkus-hibernate-validator` extension is needed:

```xml
<dependency>
    <groupId>io.quarkus</groupId>
    <artifactId>quarkus-hibernate-validator</artifactId>
</dependency>
```

#### 2. Annotate DTOs

Example for `CreateConversationRequest`:

```java
// BEFORE
public class CreateConversationRequest {
    private String title;
    private Map<String, Object> metadata;
}

// AFTER
public class CreateConversationRequest {
    @Size(max = 500, message = "Title must be at most 500 characters")
    private String title;

    @Size(max = 50, message = "Metadata must have at most 50 keys")
    private Map<String, Object> metadata;
}
```

Example for `CreateEntryRequest`:

```java
// BEFORE
public class CreateEntryRequest {
    private String contentType;
    private List<Object> content;
    private String userId;
    // ...
}

// AFTER
public class CreateEntryRequest {
    @NotBlank(message = "contentType is required")
    @Size(max = 127, message = "contentType must be at most 127 characters")
    private String contentType;

    @NotNull(message = "content is required")
    @Size(min = 1, max = 1000, message = "content must have between 1 and 1000 elements")
    private List<Object> content;

    @Size(max = 255, message = "userId must be at most 255 characters")
    private String userId;

    @Size(max = 255, message = "clientId must be at most 255 characters")
    private String clientId;

    @Size(max = 100000, message = "indexedContent must be at most 100000 characters")
    private String indexedContent;
    // ...
}
```

#### 3. Add `@Valid` to Resource Methods

```java
// BEFORE
public Response createEntry(@PathParam("conversationId") UUID id, CreateEntryRequest request) {

// AFTER
public Response createEntry(@PathParam("conversationId") UUID id, @Valid CreateEntryRequest request) {
```

#### 4. Custom Metadata Size Validator (Optional)

For enforcing total serialized metadata size, create a custom constraint:

```java
@Target({FIELD, PARAMETER})
@Retention(RUNTIME)
@Constraint(validatedBy = MetadataSizeValidator.class)
public @interface MaxSerializedSize {
    String message() default "Serialized size exceeds maximum";
    int value() default 16384; // 16KB
    Class<?>[] groups() default {};
    Class<? extends Payload>[] payload() default {};
}
```

#### 5. Update OpenAPI Spec

Add constraints to schema definitions in `openapi.yml`:

```yaml
# BEFORE
CreateConversationRequest:
  type: object
  properties:
    title:
      type: string
      nullable: true

# AFTER
CreateConversationRequest:
  type: object
  properties:
    title:
      type: string
      nullable: true
      maxLength: 500
```

### Admin API Limits

Admin endpoints (`openapi-admin.yml`) may use higher limits for `limit` parameters (e.g., `@Max(1000)`) since admin queries are expected to be less frequent and may need larger result sets. The current default of `100` for admin pagination is reasonable.

## Testing

### Unit Tests

Test validation annotations using the `Validator` API directly:

```java
class CreateEntryRequestValidationTest {

    private static Validator validator;

    @BeforeAll
    static void setup() {
        validator = Validation.buildDefaultValidatorFactory().getValidator();
    }

    @Test
    void validRequest() {
        CreateEntryRequest request = new CreateEntryRequest();
        request.setContentType("text/plain");
        request.setContent(List.of(Map.of("role", "USER", "text", "hello")));
        Set<ConstraintViolation<CreateEntryRequest>> violations = validator.validate(request);
        assertTrue(violations.isEmpty());
    }

    @Test
    void missingContentType() {
        CreateEntryRequest request = new CreateEntryRequest();
        request.setContent(List.of(Map.of("role", "USER", "text", "hello")));
        Set<ConstraintViolation<CreateEntryRequest>> violations = validator.validate(request);
        assertFalse(violations.isEmpty());
        assertTrue(violations.stream().anyMatch(v -> v.getPropertyPath().toString().equals("contentType")));
    }

    @Test
    void titleTooLong() {
        CreateConversationRequest request = new CreateConversationRequest();
        request.setTitle("x".repeat(501));
        Set<ConstraintViolation<CreateConversationRequest>> violations = validator.validate(request);
        assertFalse(violations.isEmpty());
    }

    @Test
    void contentTooManyElements() {
        CreateEntryRequest request = new CreateEntryRequest();
        request.setContentType("text/plain");
        request.setContent(IntStream.range(0, 1001).mapToObj(i -> (Object) Map.of("i", i)).toList());
        Set<ConstraintViolation<CreateEntryRequest>> violations = validator.validate(request);
        assertFalse(violations.isEmpty());
    }
}
```

### Cucumber Integration Tests

Add scenarios to validate error responses:

```gherkin
Scenario: Creating a conversation with too-long title returns 400
  Given Alice has an access token
  When Alice creates a conversation with a title of 501 characters
  Then the response status is 400
  And the response contains error code "validation_error"

Scenario: Creating an entry without contentType returns 400
  Given Alice has a conversation
  When Alice creates an entry without contentType
  Then the response status is 400
  And the response contains error code "validation_error"
  And the response contains violation field "contentType"

Scenario: Search with empty query returns 400
  Given Alice has a conversation with entries
  When Alice searches with an empty query
  Then the response status is 400
```

### Existing Test Compatibility

All existing tests pass valid data, so adding validation should not break them. Run the full suite to confirm:

```bash
./mvnw test -pl memory-service > test.log 2>&1
```

## Files to Modify

| File | Change |
|------|--------|
| `memory-service/pom.xml` | Add `quarkus-hibernate-validator` dependency |
| `memory-service/.../api/dto/CreateConversationRequest.java` | Add `@Size` on title and metadata |
| `memory-service/.../api/dto/CreateEntryRequest.java` | Add `@NotBlank`, `@Size`, `@Pattern` annotations |
| `memory-service/.../api/dto/SearchEntriesRequest.java` | Add `@NotBlank`, `@Size`, `@Min`, `@Max` |
| `memory-service/.../api/dto/ShareConversationRequest.java` | Add `@NotBlank`, `@Size` |
| `memory-service/.../api/dto/CreateOwnershipTransferRequest.java` | Add `@NotNull`, `@NotBlank`, `@Size` |
| `memory-service/.../api/dto/IndexEntryRequest.java` | Add `@NotBlank`, `@Size` |
| `memory-service/.../api/dto/UpdateConversationRequest.java` | Add `@Size` on title |
| `memory-service/.../api/ConversationResource.java` | Add `@Valid` to request parameters |
| `memory-service/.../api/SharingResource.java` | Add `@Valid` to request parameters |
| `memory-service/.../api/SearchResource.java` | Add `@Valid` to request parameters |
| `memory-service/.../api/AdminResource.java` | Add `@Valid` to request parameters |
| `memory-service/.../api/GlobalExceptionMapper.java` | Add `ConstraintViolationException` mapper |
| `memory-service-contracts/.../openapi.yml` | Add `maxLength`, `minLength`, `pattern` to schemas |
| `memory-service-contracts/.../openapi-admin.yml` | Add `maxLength`, `minLength`, `pattern` to schemas |
| `memory-service/.../test/.../validation/*` | **New**: Validation unit tests |
| `memory-service/.../test/.../features/*.feature` | Add validation error scenarios |

## Verification

```bash
# Compile
./mvnw compile

# Run validation unit tests
./mvnw test -pl memory-service -Dtest="*ValidationTest"

# Run full test suite
./mvnw test -pl memory-service > test.log 2>&1
# Search for failures using Grep tool on test.log
```

## Design Decisions

1. **Jakarta Bean Validation over manual checks**: Declarative annotations are self-documenting, automatically wire into Quarkus error handling, and keep validation rules co-located with the fields they protect.
2. **Custom error mapper**: Aligns validation errors with the project's `ErrorResponse` format (`{error, code, details}`) rather than the default Quarkus violation format.
3. **Generous initial limits**: The proposed limits (500-char titles, 255-char user IDs, 100K-char indexed content) are intentionally generous for a first pass. They can be tightened based on production usage data.
4. **Metadata size limit via serialized size**: Checking total JSONB size rather than individual value lengths prevents abuse through many small keys or deeply nested values.
5. **OpenAPI spec alignment**: Updating the spec ensures generated clients enforce limits client-side, reducing round trips for invalid requests.
