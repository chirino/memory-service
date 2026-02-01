# Hybrid Highlight Generation

## Motivation

When the batch indexing job transforms content before indexing (e.g., redacting PII, summarizing), the indexed text differs from the original entry content. This creates a challenge for generating useful search highlights:

- **Indexed text highlights** may contain redaction tokens like `[REDACTED]` or `[NAME]`
- **Original content highlights** would expose the sensitive data that was intentionally redacted
- Users expect highlights to show meaningful context around the match

This enhancement proposes a hybrid approach that generates highlights from both the indexed text and original content, avoiding redaction tokens while preserving context.

## Dependencies

- **Enhancement 030 (Index Endpoint Redesign)**: Per-entry indexing with separate indexed text and original content.

## Design Decisions

### Hybrid Highlight Algorithm

The algorithm builds highlights by expanding from the match position, switching to original content when redaction tokens are encountered.

**Algorithm:**

```
1. Find match position in indexed text
2. Initialize highlight window around match
3. Expand left from match:
   a. If next character is part of a redaction token → stop left expansion
   b. Otherwise → include character, continue
4. Expand right from match:
   a. If next character is part of a redaction token → stop right expansion
   b. Otherwise → include character, continue
5. If expansion stopped due to redaction token:
   a. Take the "clean portion" (text between match and redaction token)
   b. Search for clean portion in original content
   c. If found → continue expanding from that position in original
   d. If not found → use indexed text highlight as-is
6. Return final highlight
```

### Example Walkthrough

**Input:**
```
Indexed text:  "User shared [REDACTED] with the agent about their account"
Original text: "User shared their SSN 123-45-6789 with the agent about their account"
Query:         "agent"
Target size:   50 characters
```

**Step-by-step:**

| Step | Action | Result |
|------|--------|--------|
| 1 | Find "agent" in indexed | Position 37 |
| 2 | Initialize window | "agent" |
| 3 | Expand left | "with the agent" (hit "[REDACTED]", stop) |
| 4 | Expand right | "with the agent about their account" |
| 5a | Clean portion | "with the agent" |
| 5b | Find in original | Position 35 |
| 5c | Continue in original | "SSN 123-45-6789 with the agent about their" |
| 6 | Return | "SSN 123-45-6789 with the agent about their" |

### Redaction Token Detection

The algorithm needs to recognize redaction tokens. These are configurable patterns that indicate transformed content.

**Default patterns:**
```
[REDACTED]
[NAME]
[EMAIL]
[PHONE]
[SSN]
[ADDRESS]
[DATE]
[MASKED]
```

**Configuration:**
```properties
memory-service.search.redaction-tokens=[REDACTED],[NAME],[EMAIL],[PHONE],[SSN],[ADDRESS]
```

### Fallback Behavior

When the algorithm cannot find the clean portion in original content:

| Scenario | Fallback |
|----------|----------|
| Clean portion too short (< 10 chars) | Use indexed text highlight |
| Clean portion not found in original | Use indexed text highlight |
| Multiple matches in original | Use first match |
| No redaction tokens encountered | Use indexed text directly |
| Original content not available | Use indexed text highlight |

### Configuration Options

| Property | Default | Description |
|----------|---------|-------------|
| `memory-service.search.highlight-size` | 100 | Target highlight length in characters |
| `memory-service.search.min-clean-portion` | 10 | Minimum clean portion length for original lookup |
| `memory-service.search.redaction-tokens` | (see above) | Comma-separated list of redaction token patterns |
| `memory-service.search.use-hybrid-highlights` | true | Enable/disable hybrid algorithm |

## API Changes

No API changes required. The `highlights` field in `SearchResult` continues to return a string snippet. The change is purely in how that snippet is generated.

### Behavior Change

| Aspect | Before | After |
|--------|--------|-------|
| Highlight source | Original entry content only | Hybrid: indexed + original |
| Redaction tokens in highlights | N/A (no redaction) | Avoided when possible |
| Original content exposure | Always | Only for non-redacted portions |

## Implementation

### Pseudocode

```java
public String generateHighlight(String indexedText, String originalText,
                                 String query, int targetSize) {
    // Find match in indexed text
    int matchPos = indexedText.toLowerCase().indexOf(query.toLowerCase());
    if (matchPos < 0) return null;

    // Expand in indexed text until redaction token or target size
    int left = matchPos;
    int right = matchPos + query.length();
    boolean hitRedactionLeft = false;
    boolean hitRedactionRight = false;

    while (right - left < targetSize) {
        // Try expanding left
        if (left > 0 && !hitRedactionLeft) {
            String prefix = indexedText.substring(Math.max(0, left - 20), left);
            if (containsRedactionToken(prefix)) {
                hitRedactionLeft = true;
                left = findTokenBoundary(indexedText, left, -1);
            } else {
                left--;
            }
        }

        // Try expanding right
        if (right < indexedText.length() && !hitRedactionRight) {
            String suffix = indexedText.substring(right, Math.min(indexedText.length(), right + 20));
            if (containsRedactionToken(suffix)) {
                hitRedactionRight = true;
                right = findTokenBoundary(indexedText, right, 1);
            } else {
                right++;
            }
        }

        // Stop if can't expand either direction
        if ((left == 0 || hitRedactionLeft) &&
            (right == indexedText.length() || hitRedactionRight)) {
            break;
        }
    }

    String indexedHighlight = indexedText.substring(left, right);

    // If we hit redaction and have original, try hybrid approach
    if ((hitRedactionLeft || hitRedactionRight) && originalText != null) {
        return buildHybridHighlight(indexedHighlight, originalText, query, targetSize,
                                    hitRedactionLeft, hitRedactionRight);
    }

    return indexedHighlight.trim();
}

private String buildHybridHighlight(String indexedHighlight, String originalText,
                                     String query, int targetSize,
                                     boolean expandLeft, boolean expandRight) {
    // Find clean portion (longest substring without redaction tokens)
    String cleanPortion = extractCleanPortion(indexedHighlight, query);

    if (cleanPortion.length() < minCleanPortionLength) {
        return indexedHighlight; // Fallback
    }

    // Find clean portion in original
    int originalPos = originalText.indexOf(cleanPortion);
    if (originalPos < 0) {
        return indexedHighlight; // Fallback
    }

    // Expand in original text
    int left = originalPos;
    int right = originalPos + cleanPortion.length();

    while (right - left < targetSize) {
        if (expandLeft && left > 0) left--;
        if (expandRight && right < originalText.length()) right++;
        if (left == 0 && right == originalText.length()) break;
    }

    return originalText.substring(left, right).trim();
}
```

### Storage Requirements

The indexed text must be stored alongside the embedding for hybrid highlights to work:

```sql
-- Vector search table (updated)
CREATE TABLE entry_vectors (
    entry_id UUID PRIMARY KEY REFERENCES entries(id),
    conversation_id UUID NOT NULL REFERENCES conversations(id),
    indexed_text TEXT NOT NULL,  -- Store for highlight generation
    embedding vector(1536),
    created_at TIMESTAMP DEFAULT NOW()
);
```

This is already required for the per-entry indexing design in Enhancement 030.

## Scope of Changes

### Files Modified

| File | Change |
|------|--------|
| `memory-service/src/main/java/io/github/chirino/memory/search/HighlightGenerator.java` | New: hybrid highlight algorithm |
| `memory-service/src/main/java/io/github/chirino/memory/store/impl/PostgresMemoryStore.java` | Use HighlightGenerator |
| `memory-service/src/main/java/io/github/chirino/memory/store/impl/MongoMemoryStore.java` | Use HighlightGenerator |
| `memory-service/src/main/resources/application.properties` | Add highlight configuration |
| `memory-service/src/test/java/io/github/chirino/memory/search/HighlightGeneratorTest.java` | New: unit tests |

## Testing

### Unit Tests

```java
@Test
void noRedactionTokens_usesIndexedText() {
    String indexed = "The quick brown fox jumps over the lazy dog";
    String original = indexed; // Same content

    String highlight = generator.generateHighlight(indexed, original, "fox", 30);

    assertThat(highlight).contains("fox");
    assertThat(highlight.length()).isLessThanOrEqualTo(35);
}

@Test
void redactionOnLeft_usesOriginalForLeftContext() {
    String indexed = "User shared [REDACTED] with the agent";
    String original = "User shared their SSN 123-45-6789 with the agent";

    String highlight = generator.generateHighlight(indexed, original, "agent", 40);

    assertThat(highlight).contains("agent");
    assertThat(highlight).contains("123-45-6789"); // From original
    assertThat(highlight).doesNotContain("[REDACTED]");
}

@Test
void redactionOnBothSides_usesOriginalForBoth() {
    String indexed = "[NAME] talked to [NAME] about the project";
    String original = "Alice talked to Bob about the project";

    String highlight = generator.generateHighlight(indexed, original, "project", 30);

    assertThat(highlight).contains("project");
    assertThat(highlight).contains("Bob"); // From original
    assertThat(highlight).doesNotContain("[NAME]");
}

@Test
void cleanPortionNotFound_fallsBackToIndexed() {
    String indexed = "User shared [REDACTED] info";
    String original = "Completely different content here";

    String highlight = generator.generateHighlight(indexed, original, "info", 20);

    assertThat(highlight).contains("info");
    // Falls back to indexed since "info" context not in original
}

@Test
void originalNotAvailable_usesIndexedOnly() {
    String indexed = "User shared [REDACTED] with the agent";

    String highlight = generator.generateHighlight(indexed, null, "agent", 30);

    assertThat(highlight).contains("agent");
    assertThat(highlight).contains("[REDACTED]"); // No choice
}
```

### Integration Tests

```gherkin
Feature: Hybrid highlights in search results

  Scenario: Search with redacted content shows original context
    Given a conversation with entry content "User shared SSN 123-45-6789 with support"
    And the entry is indexed with text "User shared [REDACTED] with support"
    When I search for "support"
    Then the highlight should contain "123-45-6789"
    And the highlight should not contain "[REDACTED]"

  Scenario: Search without redaction uses indexed text
    Given a conversation with entry content "Discussion about API design"
    And the entry is indexed with text "Discussion about API design"
    When I search for "API"
    Then the highlight should contain "API design"
```

## Verification

```bash
# Run unit tests
./mvnw test -Dtest=HighlightGeneratorTest

# Run integration tests
./mvnw test -Dcucumber.filter.tags="@hybrid-highlights"

# Manual verification
curl -X POST /v1/conversations/search \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"query": "support"}'

# Check that highlights avoid redaction tokens
```

## Performance Considerations

| Operation | Impact |
|-----------|--------|
| String search in original | O(n) where n = original text length |
| Redaction token detection | O(m * k) where m = text length, k = number of patterns |
| Overall highlight generation | Negligible compared to vector search |

The hybrid algorithm adds minimal overhead since:
- It only runs when redaction tokens are detected
- String operations are fast for typical entry sizes (< 10KB)
- Original text is already fetched for `includeEntry=true` requests

## Future Considerations

- **Regex-based token detection**: Support regex patterns for more flexible token matching
- **Multiple clean portions**: Handle cases with redaction tokens in the middle of the match
- **Highlight caching**: Cache generated highlights for frequently searched entries
- **Token-aware word boundaries**: Expand to word boundaries rather than characters
