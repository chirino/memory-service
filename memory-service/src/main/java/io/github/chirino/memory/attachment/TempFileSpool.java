package io.github.chirino.memory.attachment;

import java.io.FilterOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.io.RandomAccessFile;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.locks.Condition;
import java.util.concurrent.locks.Lock;
import java.util.concurrent.locks.ReentrantLock;

/**
 * Utility that spools data from a producer to a temp file in a background virtual thread, returning
 * an InputStream that reads from the temp file concurrently as it is being written.
 *
 * <p>The producer writes to the provided OutputStream; the OutputStream auto-flushes every 32 KB
 * and signals the reader that more data is available. The returned InputStream deletes the temp file
 * when closed.
 */
final class TempFileSpool {

    private static final int AUTO_FLUSH_BYTES = 32 * 1024;

    @FunctionalInterface
    interface Producer {
        void produce(OutputStream out) throws Exception;
    }

    /**
     * Starts a background virtual thread that invokes the producer, writing to a temp file. Returns
     * an InputStream that reads from the temp file concurrently.
     */
    static InputStream spool(Producer producer) throws IOException {
        Path tempFile = Files.createTempFile("attachment-spool-", ".tmp");
        tempFile.toFile().deleteOnExit();

        SpoolState state = new SpoolState();

        Thread.startVirtualThread(
                () -> {
                    try (OutputStream out =
                            new TrackingOutputStream(Files.newOutputStream(tempFile), state)) {
                        producer.produce(out);
                    } catch (Exception e) {
                        state.setError(e);
                    } finally {
                        state.setDone();
                    }
                });

        return new SpoolInputStream(tempFile, state);
    }

    /** Shared state between the writer (virtual thread) and reader (SpoolInputStream). */
    static final class SpoolState {

        private final Lock lock = new ReentrantLock();
        private final Condition dataAvailable = lock.newCondition();
        private volatile long bytesWritten;
        private volatile boolean done;
        private volatile Throwable error;

        void addBytes(long n) {
            lock.lock();
            try {
                bytesWritten += n;
                dataAvailable.signalAll();
            } finally {
                lock.unlock();
            }
        }

        void setDone() {
            lock.lock();
            try {
                done = true;
                dataAvailable.signalAll();
            } finally {
                lock.unlock();
            }
        }

        void setError(Throwable e) {
            lock.lock();
            try {
                error = e;
                done = true;
                dataAvailable.signalAll();
            } finally {
                lock.unlock();
            }
        }

        void awaitData(long timeoutMs) throws InterruptedException {
            lock.lock();
            try {
                if (!done) {
                    dataAvailable.await(timeoutMs, TimeUnit.MILLISECONDS);
                }
            } finally {
                lock.unlock();
            }
        }
    }

    /**
     * OutputStream wrapper that tracks bytes written and auto-flushes periodically, signaling the
     * SpoolState so the concurrent reader knows more data is available.
     */
    static final class TrackingOutputStream extends FilterOutputStream {

        private final SpoolState state;
        private long unflushed;

        TrackingOutputStream(OutputStream out, SpoolState state) {
            super(out);
            this.state = state;
        }

        @Override
        public void write(int b) throws IOException {
            out.write(b);
            unflushed++;
            autoFlush();
        }

        @Override
        public void write(byte[] b, int off, int len) throws IOException {
            out.write(b, off, len);
            unflushed += len;
            autoFlush();
        }

        @Override
        public void flush() throws IOException {
            out.flush();
            if (unflushed > 0) {
                state.addBytes(unflushed);
                unflushed = 0;
            }
        }

        private void autoFlush() throws IOException {
            if (unflushed >= AUTO_FLUSH_BYTES) {
                flush();
            }
        }
    }

    /**
     * InputStream that reads from a temp file being concurrently written to. Blocks when the reader
     * catches up to the writer, waiting for more data or for the writer to finish.
     */
    static final class SpoolInputStream extends InputStream {

        private final Path tempFile;
        private final SpoolState state;
        private final RandomAccessFile raf;
        private long readPosition;

        SpoolInputStream(Path tempFile, SpoolState state) throws IOException {
            this.tempFile = tempFile;
            this.state = state;
            this.raf = new RandomAccessFile(tempFile.toFile(), "r");
        }

        @Override
        public int read() throws IOException {
            byte[] b = new byte[1];
            int n = read(b, 0, 1);
            return n == -1 ? -1 : b[0] & 0xFF;
        }

        @Override
        public int read(byte[] b, int off, int len) throws IOException {
            while (true) {
                long available = state.bytesWritten - readPosition;
                if (available > 0) {
                    int toRead = (int) Math.min(len, available);
                    raf.seek(readPosition);
                    int n = raf.read(b, off, toRead);
                    if (n > 0) {
                        readPosition += n;
                    }
                    return n;
                }

                if (state.done) {
                    checkError();
                    // Final read: writer may have flushed remaining bytes
                    long finalAvailable = state.bytesWritten - readPosition;
                    if (finalAvailable > 0) {
                        int toRead = (int) Math.min(len, finalAvailable);
                        raf.seek(readPosition);
                        int n = raf.read(b, off, toRead);
                        if (n > 0) {
                            readPosition += n;
                        }
                        return n;
                    }
                    return -1;
                }

                try {
                    state.awaitData(100);
                } catch (InterruptedException e) {
                    Thread.currentThread().interrupt();
                    throw new IOException("Interrupted while reading spool", e);
                }
            }
        }

        @Override
        public void close() throws IOException {
            raf.close();
            try {
                Files.deleteIfExists(tempFile);
            } catch (IOException ignored) {
                // Safety net: deleteOnExit will handle it
            }
        }

        private void checkError() throws IOException {
            if (state.error != null) {
                throw new IOException("Spool writer failed", state.error);
            }
        }
    }

    private TempFileSpool() {}
}
