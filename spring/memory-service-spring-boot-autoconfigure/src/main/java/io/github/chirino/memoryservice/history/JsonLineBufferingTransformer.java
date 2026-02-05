package io.github.chirino.memoryservice.history;

import java.util.ArrayList;
import java.util.List;
import reactor.core.publisher.Flux;

/**
 * Transforms a Flux&lt;String&gt; stream of arbitrary chunks into a stream of complete JSON lines.
 *
 * <p>The underlying resumer may emit chunks at arbitrary boundaries. This transformer buffers
 * incoming data and emits only complete lines (delimited by newlines).
 *
 * <p>Example:
 *
 * <pre>
 * Input chunks:  '{"e":"A"}\n{"e'  then  '":"B"}\n'
 * Output:        '{"e":"A"}'  then  '{"e":"B"}'
 * </pre>
 */
public final class JsonLineBufferingTransformer {

    private JsonLineBufferingTransformer() {}

    /**
     * Transform a stream of arbitrary string chunks into a stream of complete lines.
     *
     * @param upstream the raw string stream (may have arbitrary chunk boundaries)
     * @return stream where each emission is a complete line (without the trailing newline)
     */
    public static Flux<String> bufferLines(Flux<String> upstream) {
        return upstream.scan(new BufferState(), BufferState::append)
                .flatMapIterable(BufferState::getAndClearCompleteLines)
                .concatWith(Flux.defer(() -> Flux.empty())); // placeholder for flush handling
    }

    /**
     * Alternative implementation using windowUntil for cleaner handling of the final flush.
     *
     * @param upstream the raw string stream
     * @return stream of complete lines
     */
    public static Flux<String> bufferLinesWithFlush(Flux<String> upstream) {
        return Flux.create(
                sink -> {
                    StringBuilder buffer = new StringBuilder();

                    upstream.subscribe(
                            chunk -> {
                                if (chunk == null || chunk.isEmpty()) {
                                    return;
                                }
                                buffer.append(chunk);

                                int newlinePos;
                                while ((newlinePos = buffer.indexOf("\n")) >= 0) {
                                    String line = buffer.substring(0, newlinePos);
                                    buffer.delete(0, newlinePos + 1);

                                    if (!line.isEmpty()) {
                                        sink.next(line);
                                    }
                                }
                            },
                            error -> {
                                // Flush remaining buffer on error
                                flushBuffer(buffer, sink);
                                sink.error(error);
                            },
                            () -> {
                                // Flush remaining buffer on completion
                                flushBuffer(buffer, sink);
                                sink.complete();
                            });
                });
    }

    private static void flushBuffer(
            StringBuilder buffer, reactor.core.publisher.FluxSink<String> sink) {
        if (buffer.length() > 0) {
            String remaining = buffer.toString().trim();
            if (!remaining.isEmpty()) {
                sink.next(remaining);
            }
        }
    }

    /** Internal state for scan-based implementation. */
    private static class BufferState {
        private final StringBuilder buffer = new StringBuilder();
        private final List<String> completeLines = new ArrayList<>();

        BufferState append(String chunk) {
            if (chunk == null || chunk.isEmpty()) {
                return this;
            }

            buffer.append(chunk);

            int newlinePos;
            while ((newlinePos = buffer.indexOf("\n")) >= 0) {
                String line = buffer.substring(0, newlinePos);
                buffer.delete(0, newlinePos + 1);

                if (!line.isEmpty()) {
                    completeLines.add(line);
                }
            }

            return this;
        }

        List<String> getAndClearCompleteLines() {
            List<String> result = new ArrayList<>(completeLines);
            completeLines.clear();
            return result;
        }
    }
}
