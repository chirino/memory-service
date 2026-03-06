package io.github.chirino.memory.history.runtime;

import io.smallrye.mutiny.Multi;
import io.smallrye.mutiny.subscription.MultiEmitter;
import java.util.concurrent.atomic.AtomicReference;

/**
 * Transforms a Multi&lt;String&gt; stream of arbitrary chunks into a stream of complete JSON lines.
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
    public static Multi<String> bufferLines(Multi<String> upstream) {
        return Multi.createFrom()
                .emitter(
                        emitter -> {
                            AtomicReference<StringBuilder> buffer =
                                    new AtomicReference<>(new StringBuilder());

                            upstream.subscribe()
                                    .with(
                                            chunk -> handleChunk(chunk, buffer, emitter),
                                            failure -> {
                                                flushBuffer(buffer, emitter);
                                                emitter.fail(failure);
                                            },
                                            () -> {
                                                flushBuffer(buffer, emitter);
                                                emitter.complete();
                                            });
                        });
    }

    private static void handleChunk(
            String chunk,
            AtomicReference<StringBuilder> bufferRef,
            MultiEmitter<? super String> emitter) {

        if (chunk == null || chunk.isEmpty()) {
            return;
        }

        StringBuilder buffer = bufferRef.get();
        buffer.append(chunk);

        int newlinePos;
        while ((newlinePos = buffer.indexOf("\n")) >= 0) {
            String line = buffer.substring(0, newlinePos);
            buffer.delete(0, newlinePos + 1);

            if (!line.isEmpty()) {
                emitter.emit(line);
            }
        }
    }

    private static void flushBuffer(
            AtomicReference<StringBuilder> bufferRef, MultiEmitter<? super String> emitter) {

        StringBuilder buffer = bufferRef.get();
        if (buffer.length() > 0) {
            String remaining = buffer.toString().trim();
            if (!remaining.isEmpty()) {
                // Emit any remaining content (incomplete line at end of stream)
                emitter.emit(remaining);
            }
        }
    }
}
