package io.github.chirino.memoryservice.history;

import static org.assertj.core.api.Assertions.assertThat;

import java.util.List;
import org.junit.jupiter.api.Test;
import reactor.core.publisher.Flux;
import reactor.test.StepVerifier;

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
        Flux<String> input = Flux.just("a\nb\n");

        // When/Then: Transform and verify
        StepVerifier.create(JsonLineBufferingTransformer.bufferLinesWithFlush(input))
                .expectNext("a")
                .expectNext("b")
                .verifyComplete();
    }

    @Test
    void testPartialChunks() {
        // Given: Chunks that split a line
        Flux<String> input = Flux.just("a\nb", "c\n");

        // When/Then: Lines are assembled correctly
        StepVerifier.create(JsonLineBufferingTransformer.bufferLinesWithFlush(input))
                .expectNext("a")
                .expectNext("bc")
                .verifyComplete();
    }

    @Test
    void testSplitAcrossBoundary() {
        // Given: JSON split across chunk boundaries
        Flux<String> input = Flux.just("{\"e\":\"A", "\"}\n");

        // When/Then: JSON is assembled correctly
        StepVerifier.create(JsonLineBufferingTransformer.bufferLinesWithFlush(input))
                .expectNext("{\"e\":\"A\"}")
                .verifyComplete();
    }

    @Test
    void testNoNewline() {
        // Given: Content without trailing newline
        Flux<String> input = Flux.just("abc");

        // When/Then: Content is emitted on completion
        StepVerifier.create(JsonLineBufferingTransformer.bufferLinesWithFlush(input))
                .expectNext("abc")
                .verifyComplete();
    }

    @Test
    void testEmptyLines() {
        // Given: Input with empty lines
        Flux<String> input = Flux.just("a\n\nb\n");

        // When/Then: Empty lines are skipped
        StepVerifier.create(JsonLineBufferingTransformer.bufferLinesWithFlush(input))
                .expectNext("a")
                .expectNext("b")
                .verifyComplete();
    }

    @Test
    void testEmptyInput() {
        // Given: Empty input
        Flux<String> input = Flux.empty();

        // When/Then: Empty output
        StepVerifier.create(JsonLineBufferingTransformer.bufferLinesWithFlush(input))
                .verifyComplete();
    }

    @Test
    void testMultipleChunksPerLine() {
        // Given: Multiple chunks that form one line
        Flux<String> input = Flux.just("{\"", "type\":", "\"test\"", "}\n");

        // When/Then: All chunks are combined
        StepVerifier.create(JsonLineBufferingTransformer.bufferLinesWithFlush(input))
                .expectNext("{\"type\":\"test\"}")
                .verifyComplete();
    }

    @Test
    void testMixedCompleteAndPartial() {
        // Given: Mix of complete lines and partial chunks
        Flux<String> input = Flux.just("line1\nli", "ne2\nline3\n");

        // When/Then: All lines are correctly identified
        StepVerifier.create(JsonLineBufferingTransformer.bufferLinesWithFlush(input))
                .expectNext("line1")
                .expectNext("line2")
                .expectNext("line3")
                .verifyComplete();
    }

    @Test
    void testJsonEventsStream() {
        // Given: Realistic JSON event stream
        Flux<String> input =
                Flux.just(
                        "{\"eventType\":\"PartialResponse\",\"chunk\":\"Hello\"}\n",
                        "{\"eventType\":\"Before",
                        "ToolExecution\",\"toolName\":\"test\"}\n",
                        "{\"eventType\":\"PartialResponse\",\"chunk\":\" World\"}\n");

        // When/Then: JSON events are properly assembled
        StepVerifier.create(JsonLineBufferingTransformer.bufferLinesWithFlush(input))
                .expectNext("{\"eventType\":\"PartialResponse\",\"chunk\":\"Hello\"}")
                .expectNext("{\"eventType\":\"BeforeToolExecution\",\"toolName\":\"test\"}")
                .expectNext("{\"eventType\":\"PartialResponse\",\"chunk\":\" World\"}")
                .verifyComplete();
    }

    @Test
    void testEmptyChunks() {
        // Given: Stream with empty string chunks
        Flux<String> input = Flux.just("a\n", "", "b\n");

        // When/Then: Empty chunks are handled gracefully
        StepVerifier.create(JsonLineBufferingTransformer.bufferLinesWithFlush(input))
                .expectNext("a")
                .expectNext("b")
                .verifyComplete();
    }

    @Test
    void testTrailingWhitespace() {
        // Given: Content with trailing whitespace but no newline
        Flux<String> input = Flux.just("content   ");

        // When/Then: Whitespace is trimmed on flush
        StepVerifier.create(JsonLineBufferingTransformer.bufferLinesWithFlush(input))
                .expectNext("content")
                .verifyComplete();
    }

    @Test
    void testSubscriberFailure() {
        // Given: Stream that fails after some content
        RuntimeException testError = new RuntimeException("Test error");
        Flux<String> input = Flux.concat(Flux.just("line1\n", "partial"), Flux.error(testError));

        // When/Then: Error is propagated, but buffered content is emitted first
        StepVerifier.create(JsonLineBufferingTransformer.bufferLinesWithFlush(input))
                .expectNext("line1")
                .expectNext("partial")
                .expectError(RuntimeException.class)
                .verify();
    }

    @Test
    void testSingleCharacterChunks() {
        // Given: Single character at a time
        Flux<String> input = Flux.just("a", "b", "c", "\n", "d", "\n");

        // When/Then: Characters are buffered correctly
        StepVerifier.create(JsonLineBufferingTransformer.bufferLinesWithFlush(input))
                .expectNext("abc")
                .expectNext("d")
                .verifyComplete();
    }

    @Test
    void testOnlyNewlines() {
        // Given: Only newline characters
        Flux<String> input = Flux.just("\n\n\n");

        // When/Then: Empty lines are skipped
        StepVerifier.create(JsonLineBufferingTransformer.bufferLinesWithFlush(input))
                .verifyComplete();
    }

    @Test
    void testLongLine() {
        // Given: A very long line split into chunks
        String longContent = "x".repeat(10000);
        Flux<String> input =
                Flux.just(longContent.substring(0, 5000), longContent.substring(5000) + "\n");

        // When/Then: Long line is handled correctly
        List<String> result =
                JsonLineBufferingTransformer.bufferLinesWithFlush(input).collectList().block();

        assertThat(result).hasSize(1);
        assertThat(result.get(0)).hasSize(10000);
        assertThat(result.get(0)).isEqualTo(longContent);
    }

    @Test
    void testHandlesEmptyChunksBetweenContent() {
        // Given: Stream with empty chunks between content (simulating sparse stream)
        Flux<String> input = Flux.just("a\n", "", "", "b\n");

        // When/Then: Empty chunks are handled gracefully
        StepVerifier.create(JsonLineBufferingTransformer.bufferLinesWithFlush(input))
                .expectNext("a")
                .expectNext("b")
                .verifyComplete();
    }
}
