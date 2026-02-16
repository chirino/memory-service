package io.github.chirino.memory.resumer;

import io.smallrye.mutiny.Multi;
import io.smallrye.mutiny.subscription.MultiEmitter;
import java.io.BufferedOutputStream;
import java.io.FileOutputStream;
import java.io.IOException;
import java.io.RandomAccessFile;
import java.net.InetAddress;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.Paths;
import java.time.Duration;
import java.time.Instant;
import java.util.List;
import java.util.Optional;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.ScheduledFuture;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.stream.Collectors;
import org.jboss.logging.Logger;

public class TempFileResumerBackend implements ResponseResumerBackend {

    private static final Logger LOG = Logger.getLogger(TempFileResumerBackend.class);
    private static final String TEMP_FILE_PREFIX = "response-resume-";
    private static final String TEMP_FILE_SUFFIX = ".tokens";

    private final ResponseResumerLocatorStore locatorStore;
    private final Duration locatorTtl;
    private final Duration locatorRefresh;
    private final Path tempDir;
    private final Duration tempFileRetention;
    private final AdvertisedAddress configuredAdvertisedAddress;
    private final TempFileInFlightRegistry registry = new TempFileInFlightRegistry();
    private final ScheduledExecutorService scheduler =
            Executors.newSingleThreadScheduledExecutor(
                    runnable -> {
                        Thread thread = new Thread(runnable, "response-resumer-scheduler");
                        thread.setDaemon(true);
                        return thread;
                    });
    private final ExecutorService replayExecutor =
            Executors.newCachedThreadPool(
                    runnable -> {
                        Thread thread = new Thread(runnable, "response-resumer-replay");
                        thread.setDaemon(true);
                        return thread;
                    });

    public TempFileResumerBackend(
            ResponseResumerLocatorStore locatorStore,
            Duration locatorTtl,
            Duration locatorRefresh,
            Optional<String> tempDir,
            Duration tempFileRetention,
            Optional<String> advertisedAddress) {
        this.locatorStore = locatorStore;
        this.locatorTtl = locatorTtl;
        this.locatorRefresh = locatorRefresh;
        this.tempDir = resolveTempDir(tempDir);
        this.tempFileRetention = tempFileRetention;
        this.configuredAdvertisedAddress =
                advertisedAddress.flatMap(AdvertisedAddress::parse).orElse(null);
    }

    public void start() {
        cleanupStaleTempFiles();
        scheduler.scheduleAtFixedRate(
                this::cleanupClosedEntries,
                locatorRefresh.toMillis(),
                locatorRefresh.toMillis(),
                TimeUnit.MILLISECONDS);
    }

    public void stop() {
        scheduler.shutdownNow();
        replayExecutor.shutdownNow();
    }

    @Override
    public boolean enabled() {
        return locatorStore.available();
    }

    @Override
    public boolean hasResponseInProgress(String conversationId) {
        return locatorStore.exists(conversationId);
    }

    @Override
    public ResponseRecorder recorder(String conversationId) {
        return recorder(conversationId, null);
    }

    @Override
    public ResponseRecorder recorder(String conversationId, AdvertisedAddress advertisedAddress) {
        TempFileRegistryEntry entry = registry.register(conversationId, createTempFile());
        if (entry == null) {
            return new NoopResponseRecorder();
        }

        AdvertisedAddress resolvedAddress =
                advertisedAddress != null ? advertisedAddress : resolveFallbackAddress();
        if (resolvedAddress == null || resolvedAddress.port() <= 0) {
            LOG.warnf(
                    "Unable to resolve advertised address for conversationId=%s; redirects may"
                            + " fail",
                    conversationId);
        }

        ResponseResumerLocator locator =
                new ResponseResumerLocator(
                        resolvedAddress == null ? "localhost" : resolvedAddress.host(),
                        resolvedAddress == null ? 0 : resolvedAddress.port(),
                        entry.fileName());
        LOG.infof(
                "Recording registered: conversationId=%s advertisedAddress=%s locator=%s",
                conversationId, resolvedAddress, locator.encode());
        ScheduledFuture<?> refreshTask =
                scheduler.scheduleAtFixedRate(
                        () -> locatorStore.upsert(conversationId, locator, locatorTtl),
                        0,
                        locatorRefresh.toMillis(),
                        TimeUnit.MILLISECONDS);

        return new TempFileResponseRecorder(conversationId, entry, locator, refreshTask);
    }

    @Override
    public Multi<String> replay(String conversationId) {
        return replay(conversationId, null);
    }

    @Override
    public Multi<String> replay(String conversationId, AdvertisedAddress advertisedAddress) {

        Optional<ResponseResumerLocator> locator = locatorStore.get(conversationId);
        if (locator.isEmpty()) {
            LOG.infof("Replay: no locator found for conversationId=%s", conversationId);
            return Multi.createFrom().empty();
        }

        LOG.infof(
                "Replay: conversationId=%s advertisedAddress=%s locator=%s matches=%b",
                conversationId,
                advertisedAddress,
                locator.get().encode(),
                advertisedAddress == null || locator.get().matches(advertisedAddress));

        if (advertisedAddress != null && !locator.get().matches(advertisedAddress)) {
            LOG.infof(
                    "Replay: redirecting conversationId=%s to %s",
                    conversationId, locator.get().address());
            return Multi.createFrom()
                    .failure(new ResponseResumerRedirectException(locator.get().address()));
        }

        TempFileRegistryEntry entry = registry.get(conversationId);
        if (entry == null) {
            return Multi.createFrom().empty();
        }

        return Multi.createFrom()
                .emitter(
                        emitter ->
                                replayExecutor.execute(
                                        () -> replayFromFile(conversationId, entry, emitter)));
    }

    @Override
    public void requestCancel(String conversationId) {
        requestCancel(conversationId, null);
    }

    @Override
    public void requestCancel(String conversationId, AdvertisedAddress advertisedAddress) {
        Optional<ResponseResumerLocator> locator = locatorStore.get(conversationId);
        if (locator.isPresent()
                && advertisedAddress != null
                && !locator.get().matches(advertisedAddress)) {
            LOG.infof(
                    "Cancel-response redirect: conversationId=%s advertised=%s locator=%s",
                    conversationId, advertisedAddress, locator.get().address());
            throw new ResponseResumerRedirectException(locator.get().address());
        }

        TempFileRegistryEntry entry = registry.get(conversationId);
        if (entry != null) {
            entry.requestCancel();
        }
    }

    @Override
    public Multi<CancelSignal> cancelStream(String conversationId) {
        TempFileRegistryEntry entry = registry.get(conversationId);
        if (entry == null) {
            return Multi.createFrom().empty();
        }

        return Multi.createFrom()
                .emitter(emitter -> replayExecutor.execute(() -> awaitCancel(entry, emitter)));
    }

    @Override
    public List<String> check(List<String> conversationIds) {
        if (conversationIds == null || conversationIds.isEmpty()) {
            return List.of();
        }

        return conversationIds.stream()
                .filter(
                        conversationId -> {
                            try {
                                return hasResponseInProgress(conversationId);
                            } catch (Exception e) {
                                LOG.warnf(
                                        e,
                                        "Failed to check if conversation %s has response in"
                                                + " progress",
                                        conversationId);
                                return false;
                            }
                        })
                .collect(Collectors.toList());
    }

    private void replayFromFile(
            String conversationId,
            TempFileRegistryEntry entry,
            MultiEmitter<? super String> emitter) {
        AtomicBoolean cancelled = new AtomicBoolean(false);
        emitter.onTermination(() -> cancelled.set(true));

        entry.incrementReaders();
        try (RandomAccessFile input = new RandomAccessFile(entry.filePath().toFile(), "r")) {
            long readOffset = 0;
            long readByteOffset = 0;
            boolean waitingForBytes = false;

            while (!cancelled.get()) {
                if (!waitingForBytes
                        && !entry.isClosed()
                        && entry.lastWrittenByteOffset() <= readByteOffset) {
                    waitingForBytes = true;
                }
                if (!entry.awaitBytes(readByteOffset + 1, cancelled)) {
                    if (entry.isClosed() && readOffset >= entry.finalOffset()) {
                        emitter.complete();
                    }
                    break;
                }
                if (waitingForBytes) {
                    waitingForBytes = false;
                }

                long availableBytes = entry.lastWrittenByteOffset();
                if (availableBytes <= readByteOffset) {
                    continue;
                }

                long chunkSize = availableBytes - readByteOffset;
                if (chunkSize > Integer.MAX_VALUE) {
                    chunkSize = Integer.MAX_VALUE;
                }

                long tokenStartOffset = readOffset;

                byte[] tokenData = new byte[(int) chunkSize];
                input.seek(readByteOffset);
                input.readFully(tokenData);

                String token = new String(tokenData, StandardCharsets.UTF_8);
                long tokenEndOffset = tokenStartOffset + token.length();
                readOffset = tokenEndOffset;
                readByteOffset += chunkSize;

                emitter.emit(token);

                if (entry.isClosed() && readOffset >= entry.finalOffset()) {
                    emitter.complete();
                    break;
                }
            }
        } catch (Exception e) {
            emitter.fail(e);
        } finally {
            entry.decrementReaders();
            registry.cleanupIfPossible(conversationId, entry);
        }
    }

    private void awaitCancel(
            TempFileRegistryEntry entry, MultiEmitter<? super CancelSignal> emitter) {
        if (entry.isCancelRequested()) {
            emitter.emit(CancelSignal.CANCEL_SIGNAL);
            emitter.complete();
            return;
        }

        AtomicBoolean cancelled = new AtomicBoolean(false);
        emitter.onTermination(() -> cancelled.set(true));

        boolean cancelRequested = entry.awaitCancel(cancelled);
        if (cancelRequested) {
            emitter.emit(CancelSignal.CANCEL_SIGNAL);
        }
        emitter.complete();
    }

    private void cleanupClosedEntries() {
        registry.cleanupClosedEntries();
    }

    private void cleanupStaleTempFiles() {
        try {
            if (!Files.exists(tempDir)) {
                Files.createDirectories(tempDir);
            }
        } catch (IOException e) {
            LOG.warnf(e, "Failed to ensure response resumer temp directory %s exists", tempDir);
            return;
        }

        Instant cutoff = Instant.now().minus(tempFileRetention);
        try {
            Files.list(tempDir)
                    .filter(path -> path.getFileName().toString().startsWith(TEMP_FILE_PREFIX))
                    .filter(path -> path.getFileName().toString().endsWith(TEMP_FILE_SUFFIX))
                    .forEach(
                            path -> {
                                try {
                                    if (Files.getLastModifiedTime(path)
                                            .toInstant()
                                            .isBefore(cutoff)) {
                                        Files.deleteIfExists(path);
                                    }
                                } catch (IOException e) {
                                    LOG.debugf(
                                            e,
                                            "Failed to delete stale response temp file %s",
                                            path);
                                }
                            });
        } catch (IOException e) {
            LOG.debugf(e, "Failed to scan response resumer temp directory %s", tempDir);
        }
    }

    private Path createTempFile() {
        try {
            Files.createDirectories(tempDir);
            return Files.createTempFile(tempDir, TEMP_FILE_PREFIX, TEMP_FILE_SUFFIX);
        } catch (IOException e) {
            LOG.warnf(e, "Failed to create response resumer temp file in %s", tempDir);
            return null;
        }
    }

    private static Path resolveTempDir(Optional<String> tempDir) {
        if (tempDir.isPresent()) {
            return Paths.get(tempDir.get());
        }
        return Paths.get(System.getProperty("java.io.tmpdir"));
    }

    private AdvertisedAddress resolveFallbackAddress() {
        if (configuredAdvertisedAddress != null) {
            return configuredAdvertisedAddress;
        }
        try {
            String host = InetAddress.getLocalHost().getHostName();
            return new AdvertisedAddress(host, 0);
        } catch (Exception e) {
            return new AdvertisedAddress("localhost", 0);
        }
    }

    private final class TempFileResponseRecorder implements ResponseRecorder {
        private final String conversationId;
        private final TempFileRegistryEntry entry;
        private final ResponseResumerLocator locator;
        private final ScheduledFuture<?> refreshTask;
        private final AtomicBoolean completed = new AtomicBoolean(false);
        private final AtomicBoolean closed = new AtomicBoolean(false);
        private final Object writeLock = new Object();
        private BufferedOutputStream output;

        TempFileResponseRecorder(
                String conversationId,
                TempFileRegistryEntry entry,
                ResponseResumerLocator locator,
                ScheduledFuture<?> refreshTask) {
            this.conversationId = conversationId;
            this.entry = entry;
            this.locator = locator;
            this.refreshTask = refreshTask;
            this.output = openOutput(entry.filePath());
            locatorStore.upsert(conversationId, locator, locatorTtl);
        }

        @Override
        public void record(String token) {
            if (token == null || token.isEmpty() || completed.get()) {
                return;
            }
            byte[] bytes = token.getBytes(StandardCharsets.UTF_8);
            synchronized (writeLock) {
                if (completed.get() || output == null) {
                    return;
                }
                try {
                    output.write(bytes);
                    output.flush();
                    entry.append(token.length(), bytes.length);
                } catch (IOException e) {
                    LOG.warnf(e, "Failed to append token for conversationId=%s", conversationId);
                }
            }
        }

        @Override
        public void complete() {
            if (!completed.compareAndSet(false, true)) {
                return;
            }
            closeWriter();
            entry.markClosed();
            locatorStore.remove(conversationId);
            if (refreshTask != null) {
                refreshTask.cancel(false);
            }
            registry.cleanupIfPossible(conversationId, entry);
        }

        private BufferedOutputStream openOutput(Path path) {
            if (path == null) {
                return null;
            }
            try {
                return new BufferedOutputStream(new FileOutputStream(path.toFile(), true));
            } catch (IOException e) {
                LOG.warnf(e, "Failed to open response resumer temp file %s", path);
                return null;
            }
        }

        private void closeWriter() {
            if (!closed.compareAndSet(false, true)) {
                return;
            }
            synchronized (writeLock) {
                if (output == null) {
                    entry.decrementWriters();
                    return;
                }
                try {
                    output.close();
                } catch (IOException e) {
                    LOG.debugf(e, "Failed to close response temp file for %s", conversationId);
                } finally {
                    output = null;
                    entry.decrementWriters();
                }
            }
        }
    }
}
