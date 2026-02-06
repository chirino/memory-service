package io.github.chirino.memory.history.runtime;

import static org.assertj.core.api.Assertions.assertThat;

import io.smallrye.mutiny.Multi;
import io.smallrye.mutiny.helpers.test.AssertSubscriber;
import java.util.List;
import org.junit.jupiter.api.Test;

/**
 * Tests for {@link JsonLineBufferingTransformer}.
 *
 * <p>Verifies that:
 *
 * <ul>
 *   <li>Complete lines (ending with \n) are emitted immediately
 *   <li>Partial chunks are buffered until newline is received
 *   <li>Chunks split across boundaries are correctly assembled
 *   <li>Content without trailing newline is emitted on completion
 *   <li>Empty lines are skipped
 * </ul>
 */
class JsonLineBufferingTransformerTest {

    @Test
    void testCompleteLines() {
        // Given: Input with complete lines
        Multi<String> input = Multi.createFrom().items("a\nb\n");

        // When: Transform
        List<String> result =
                JsonLineBufferingTransformer.bufferLines(input).subscribe().asStream().toList();

        // Then: Each line is emitted separately
        assertThat(result).containsExactly("a", "b");
    }

    @Test
    void testPartialChunks() {
        // Given: Chunks that split a line
        Multi<String> input = Multi.createFrom().items("a\nb", "c\n");

        // When: Transform
        List<String> result =
                JsonLineBufferingTransformer.bufferLines(input).subscribe().asStream().toList();

        // Then: Lines are assembled correctly
        assertThat(result).containsExactly("a", "bc");
    }

    @Test
    void testSplitAcrossBoundary() {
        // Given: JSON split across chunk boundaries
        Multi<String> input = Multi.createFrom().items("{\"e\":\"A", "\"}\n");

        // When: Transform
        List<String> result =
                JsonLineBufferingTransformer.bufferLines(input).subscribe().asStream().toList();

        // Then: JSON is assembled correctly
        assertThat(result).containsExactly("{\"e\":\"A\"}");
    }

    @Test
    void testNoNewline() {
        // Given: Content without trailing newline
        Multi<String> input = Multi.createFrom().items("abc");

        // When: Transform
        List<String> result =
                JsonLineBufferingTransformer.bufferLines(input).subscribe().asStream().toList();

        // Then: Content is emitted on completion
        assertThat(result).containsExactly("abc");
    }

    @Test
    void testEmptyLines() {
        // Given: Input with empty lines
        Multi<String> input = Multi.createFrom().items("a\n\nb\n");

        // When: Transform
        List<String> result =
                JsonLineBufferingTransformer.bufferLines(input).subscribe().asStream().toList();

        // Then: Empty lines are skipped
        assertThat(result).containsExactly("a", "b");
    }

    @Test
    void testEmptyInput() {
        // Given: Empty input
        Multi<String> input = Multi.createFrom().empty();

        // When: Transform
        List<String> result =
                JsonLineBufferingTransformer.bufferLines(input).subscribe().asStream().toList();

        // Then: Empty output
        assertThat(result).isEmpty();
    }

    @Test
    void testMultipleChunksPerLine() {
        // Given: Multiple chunks that form one line
        Multi<String> input = Multi.createFrom().items("{\"", "type\":", "\"test\"", "}\n");

        // When: Transform
        List<String> result =
                JsonLineBufferingTransformer.bufferLines(input).subscribe().asStream().toList();

        // Then: All chunks are combined
        assertThat(result).containsExactly("{\"type\":\"test\"}");
    }

    @Test
    void testMixedCompleteAndPartial() {
        // Given: Mix of complete lines and partial chunks
        Multi<String> input = Multi.createFrom().items("line1\nli", "ne2\nline3\n");

        // When: Transform
        List<String> result =
                JsonLineBufferingTransformer.bufferLines(input).subscribe().asStream().toList();

        // Then: All lines are correctly identified
        assertThat(result).containsExactly("line1", "line2", "line3");
    }

    @Test
    void testJsonEventsStream() {
        // Given: Realistic JSON event stream
        Multi<String> input =
                Multi.createFrom()
                        .items(
                                "{\"eventType\":\"PartialResponse\",\"chunk\":\"Hello\"}\n",
                                "{\"eventType\":\"Before",
                                "ToolExecution\",\"toolName\":\"test\"}\n",
                                "{\"eventType\":\"PartialResponse\",\"chunk\":\" World\"}\n");

        // When: Transform
        List<String> result =
                JsonLineBufferingTransformer.bufferLines(input).subscribe().asStream().toList();

        // Then: JSON events are properly assembled
        assertThat(result)
                .containsExactly(
                        "{\"eventType\":\"PartialResponse\",\"chunk\":\"Hello\"}",
                        "{\"eventType\":\"BeforeToolExecution\",\"toolName\":\"test\"}",
                        "{\"eventType\":\"PartialResponse\",\"chunk\":\" World\"}");
    }

    @Test
    void testHandlesEmptyChunksBetweenContent() {
        // Given: Stream with empty chunks between content (simulating sparse stream)
        // Note: Multi doesn't allow null values, so we test with empty strings instead
        Multi<String> input = Multi.createFrom().items("a\n", "", "", "b\n");

        // When: Transform
        List<String> result =
                JsonLineBufferingTransformer.bufferLines(input).subscribe().asStream().toList();

        // Then: Empty chunks are handled gracefully
        assertThat(result).containsExactly("a", "b");
    }

    @Test
    void testEmptyChunks() {
        // Given: Stream with empty string chunks
        Multi<String> input = Multi.createFrom().items("a\n", "", "b\n");

        // When: Transform
        List<String> result =
                JsonLineBufferingTransformer.bufferLines(input).subscribe().asStream().toList();

        // Then: Empty chunks are handled gracefully
        assertThat(result).containsExactly("a", "b");
    }

    @Test
    void testTrailingWhitespace() {
        // Given: Content with trailing whitespace but no newline
        Multi<String> input = Multi.createFrom().items("content   ");

        // When: Transform
        List<String> result =
                JsonLineBufferingTransformer.bufferLines(input).subscribe().asStream().toList();

        // Then: Whitespace is trimmed on flush
        assertThat(result).containsExactly("content");
    }

    @Test
    void testSubscriberFailure() {
        // Given: Stream that fails
        RuntimeException testError = new RuntimeException("Test error");
        Multi<String> input =
                Multi.createFrom().items("line1\n", "partial").onCompletion().failWith(testError);

        // When: Subscribe with assertion
        AssertSubscriber<String> subscriber =
                JsonLineBufferingTransformer.bufferLines(input)
                        .subscribe()
                        .withSubscriber(AssertSubscriber.create(10));

        // Then: Error is propagated, but buffered content is emitted first
        subscriber.awaitFailure();
        assertThat(subscriber.getItems()).contains("line1", "partial");
        assertThat(subscriber.getFailure()).isEqualTo(testError);
    }

    @Test
    void testSingleCharacterChunks() {
        // Given: Single character at a time
        Multi<String> input = Multi.createFrom().items("a", "b", "c", "\n", "d", "\n");

        // When: Transform
        List<String> result =
                JsonLineBufferingTransformer.bufferLines(input).subscribe().asStream().toList();

        // Then: Characters are buffered correctly
        assertThat(result).containsExactly("abc", "d");
    }

    @Test
    void testOnlyNewlines() {
        // Given: Only newline characters
        Multi<String> input = Multi.createFrom().items("\n\n\n");

        // When: Transform
        List<String> result =
                JsonLineBufferingTransformer.bufferLines(input).subscribe().asStream().toList();

        // Then: Empty lines are skipped
        assertThat(result).isEmpty();
    }

    @Test
    void testLongLine() {
        // Given: A very long line split into chunks
        String longContent = "x".repeat(10000);
        Multi<String> input =
                Multi.createFrom()
                        .items(longContent.substring(0, 5000), longContent.substring(5000) + "\n");

        // When: Transform
        List<String> result =
                JsonLineBufferingTransformer.bufferLines(input).subscribe().asStream().toList();

        // Then: Long line is handled correctly
        assertThat(result).hasSize(1);
        assertThat(result.get(0)).hasSize(10000);
        assertThat(result.get(0)).isEqualTo(longContent);
    }
}
