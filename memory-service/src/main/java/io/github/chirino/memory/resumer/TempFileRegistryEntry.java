package io.github.chirino.memory.resumer;

import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Duration;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.concurrent.atomic.AtomicLong;
import java.util.concurrent.atomic.AtomicReference;
import org.jboss.logging.Logger;

public final class TempFileRegistryEntry {
    private static final Duration WAIT_TIMEOUT = Duration.ofSeconds(1);
    private static final Logger LOG = Logger.getLogger(TempFileRegistryEntry.class);

    public enum State {
        OPEN,
        CLOSED
    }

    private final Path filePath;
    private final String fileName;
    private final AtomicLong lastWrittenOffset = new AtomicLong(0);
    private final AtomicLong lastWrittenByteOffset = new AtomicLong(0);
    private final AtomicLong finalOffset = new AtomicLong(0);
    private final AtomicReference<State> state = new AtomicReference<>(State.OPEN);
    private final AtomicInteger readerCount = new AtomicInteger(0);
    private final AtomicInteger writerCount = new AtomicInteger(1);
    private final AtomicBoolean cancelRequested = new AtomicBoolean(false);
    private final Object monitor = new Object();

    TempFileRegistryEntry(Path filePath) {
        this.filePath = filePath;
        this.fileName = filePath.getFileName().toString();
    }

    Path filePath() {
        return filePath;
    }

    String fileName() {
        return fileName;
    }

    long finalOffset() {
        return finalOffset.get();
    }

    long lastWrittenOffset() {
        return lastWrittenOffset.get();
    }

    long lastWrittenByteOffset() {
        return lastWrittenByteOffset.get();
    }

    boolean isClosed() {
        return state.get() == State.CLOSED;
    }

    void append(long charDelta, long byteDelta) {
        lastWrittenOffset.addAndGet(charDelta);
        lastWrittenByteOffset.addAndGet(byteDelta);
        signalReaders();
    }

    void markClosed() {
        if (!state.compareAndSet(State.OPEN, State.CLOSED)) {
            return;
        }
        finalOffset.set(lastWrittenOffset.get());
        signalReaders();
    }

    void incrementReaders() {
        readerCount.incrementAndGet();
    }

    void decrementReaders() {
        readerCount.decrementAndGet();
    }

    void decrementWriters() {
        writerCount.decrementAndGet();
        signalReaders();
    }

    boolean isClosable() {
        return state.get() == State.CLOSED && readerCount.get() <= 0 && writerCount.get() <= 0;
    }

    boolean isCancelRequested() {
        return cancelRequested.get();
    }

    void requestCancel() {
        if (cancelRequested.compareAndSet(false, true)) {
            signalReaders();
        }
    }

    boolean awaitCancel(AtomicBoolean cancelled) {
        boolean loggedWait = false;
        synchronized (monitor) {
            while (!cancelled.get()) {
                if (cancelRequested.get()) {
                    return true;
                }
                if (state.get() == State.CLOSED) {
                    return false;
                }
                if (!loggedWait) {
                    loggedWait = true;
                }
                try {
                    monitor.wait(WAIT_TIMEOUT.toMillis());
                } catch (InterruptedException e) {
                    Thread.currentThread().interrupt();
                    return false;
                }
            }
        }
        return false;
    }

    boolean awaitBytes(long targetOffset, AtomicBoolean cancelled) {
        boolean loggedWait = false;
        synchronized (monitor) {
            while (!cancelled.get()) {
                if (lastWrittenByteOffset.get() >= targetOffset) {
                    return true;
                }
                if (state.get() == State.CLOSED) {
                    return false;
                }
                if (!loggedWait) {
                    loggedWait = true;
                }
                try {
                    monitor.wait(WAIT_TIMEOUT.toMillis());
                } catch (InterruptedException e) {
                    Thread.currentThread().interrupt();
                    return false;
                }
            }
        }
        return false;
    }

    boolean tryDelete() {
        try {
            return Files.deleteIfExists(filePath);
        } catch (IOException e) {
            LOG.debugf(e, "Failed to delete response temp file %s", filePath);
            return false;
        }
    }

    private void signalReaders() {
        synchronized (monitor) {
            monitor.notifyAll();
        }
    }
}
