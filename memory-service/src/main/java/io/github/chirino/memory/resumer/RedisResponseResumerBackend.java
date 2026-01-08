package io.github.chirino.memory.resumer;

import io.quarkus.redis.client.RedisClientName;
import io.quarkus.redis.datasource.ReactiveRedisDataSource;
import io.quarkus.redis.datasource.keys.ReactiveKeyCommands;
import io.quarkus.redis.datasource.stream.ReactiveStreamCommands;
import io.quarkus.redis.datasource.stream.StreamMessage;
import io.quarkus.redis.datasource.stream.XAddArgs;
import io.quarkus.redis.datasource.stream.XReadArgs;
import io.smallrye.mutiny.Multi;
import io.smallrye.mutiny.subscription.MultiEmitter;
import jakarta.annotation.PostConstruct;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.enterprise.inject.Any;
import jakarta.enterprise.inject.Instance;
import jakarta.inject.Inject;
import java.time.Duration;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.concurrent.ConcurrentLinkedQueue;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicLong;
import java.util.stream.Collectors;
import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.jboss.logging.Logger;

@ApplicationScoped
public class RedisResponseResumerBackend implements ResponseResumerBackend {

    private static final Logger LOG = Logger.getLogger(RedisResponseResumerBackend.class);
    private static final String STREAM_KEY_PREFIX = "conversation:response:";
    private static final String TOKEN_FIELD = "t";

    private final boolean redisEnabled;
    private final String clientName;
    private final Instance<ReactiveRedisDataSource> redisSources;

    private volatile ReactiveStreamCommands<String, String, String> stream;
    private volatile ReactiveKeyCommands<String> keys;

    @Inject
    public RedisResponseResumerBackend(
            @ConfigProperty(name = "memory-service.response-resumer") Optional<String> resumerType,
            @ConfigProperty(name = "memory-service.response-resumer.redis.client")
                    Optional<String> clientName,
            @Any Instance<ReactiveRedisDataSource> redisSources) {
        this.redisEnabled = resumerType.map("redis"::equalsIgnoreCase).orElse(false);
        this.clientName = clientName.filter(it -> !it.isBlank()).orElse(null);
        this.redisSources = redisSources;
    }

    @PostConstruct
    void init() {
        if (!redisEnabled) {
            return;
        }

        Instance<ReactiveRedisDataSource> selected =
                clientName != null
                        ? redisSources.select(RedisClientName.Literal.of(clientName))
                        : redisSources;
        if (selected.isUnsatisfied()) {
            LOG.warnf(
                    "Response resumer is enabled (memory-service.response-resumer=redis) but Redis"
                            + " client '%s' is not available. Disabling response resumption.",
                    clientName == null ? "<default>" : clientName);
            return;
        }

        stream = selected.get().stream(String.class);
        keys = selected.get().key();
    }

    @Override
    public boolean enabled() {
        return stream != null;
    }

    @Override
    public boolean hasResponseInProgress(String conversationId) {
        if (!enabled()) {
            return false;
        }
        String streamKey = streamKey(conversationId);
        return keys.exists(streamKey).await().atMost(Duration.ofSeconds(5)) == Boolean.TRUE;
    }

    @Override
    public ResponseRecorder recorder(String conversationId) {
        String streamKey = streamKey(conversationId);
        LOG.infof(
                "[REDIS] Creating recorder for conversationId=%s, streamKey=%s, enabled=%s",
                conversationId, streamKey, enabled());
        if (!enabled()) {
            return new NoopResponseRecorder();
        }
        return new RedisResponseRecorder(stream, keys, streamKey);
    }

    @Override
    public Multi<String> replay(String conversationId, long resumePosition) {
        if (!enabled()) {
            return Multi.createFrom().empty();
        }

        String streamKey = streamKey(conversationId);
        AtomicLong offset = new AtomicLong(Math.max(0L, resumePosition));

        return keys.exists(streamKey)
                .onItem()
                .transformToMulti(
                        exists -> {
                            if (exists != Boolean.TRUE) {
                                return Multi.createFrom().empty();
                            }
                            return Multi.createFrom()
                                    .emitter(
                                            emitter -> {
                                                emitter.onTermination(
                                                        () ->
                                                                LOG.debugf(
                                                                        "Stopped replaying cached"
                                                                            + " response for"
                                                                            + " conversationId=%s",
                                                                        conversationId));
                                                readFromStream(streamKey, offset, emitter);
                                            });
                        });
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

    private static String streamKey(String conversationId) {
        return STREAM_KEY_PREFIX + conversationId;
    }

    private void readFromStream(
            String streamKey, AtomicLong offset, MultiEmitter<? super String> emitter) {
        if (emitter.isCancelled()) {
            return;
        }

        String startId = offset.get() + "-0";
        stream.xread(Map.of(streamKey, startId), new XReadArgs().block(Duration.ofSeconds(1)))
                .subscribe()
                .with(
                        messages -> {
                            List<StreamMessage<String, String, String>> items =
                                    messages == null ? List.of() : messages;
                            for (StreamMessage<String, String, String> message : items) {
                                String token = message.payload().get(TOKEN_FIELD);
                                if (token == null) {
                                    continue;
                                }
                                offset.addAndGet(token.length());
                                emitter.emit(token);
                            }
                            readFromStream(streamKey, offset, emitter);
                        },
                        emitter::fail);
    }

    private static final class RedisResponseRecorder implements ResponseRecorder {
        private final ReactiveStreamCommands<String, String, String> stream;
        private final ReactiveKeyCommands<String> keys;
        private final String streamKey;
        private final AtomicLong offset = new AtomicLong(0);
        private final ConcurrentLinkedQueue<String> queue = new ConcurrentLinkedQueue<>();
        private final AtomicBoolean sending = new AtomicBoolean(false);
        private final AtomicBoolean completed = new AtomicBoolean(false);
        private final AtomicLong pendingOperations = new AtomicLong(0);
        private final AtomicBoolean deleted = new AtomicBoolean(false);

        private static final String COMPLETE_SENTINEL = "__COMPLETE__";

        RedisResponseRecorder(
                ReactiveStreamCommands<String, String, String> stream,
                ReactiveKeyCommands<String> keys,
                String streamKey) {
            this.stream = stream;
            this.keys = keys;
            this.streamKey = streamKey;
        }

        @Override
        public void record(String token) {
            if (token == null || token.isEmpty()) {
                return;
            }
            queue.add(token);
            drain();
        }

        private void drain() {
            if (!sending.compareAndSet(false, true)) {
                return;
            }
            sendNext();
        }

        private void sendNext() {
            // If we're already completed and deleted, don't process any more tokens
            if (completed.get() && deleted.get()) {
                sending.set(false);
                return;
            }

            String token = queue.poll();
            if (token == null) {
                sending.set(false);
                // If a token arrived while we were flipping the flag, start another drain so
                // nothing is stranded. But only if we're not completed.
                if (!queue.isEmpty() && !completed.get()) {
                    drain();
                } else if (completed.get() && pendingOperations.get() == 0 && !deleted.get()) {
                    // If we're completed and all operations are done, delete the stream
                    deleteStream();
                }
                return;
            }

            // Check if this is the completion sentinel
            if (COMPLETE_SENTINEL == token) {
                sending.set(false);
                // Wait for all pending operations to complete before deleting
                // Don't trigger another drain - we're done processing the queue
                if (pendingOperations.get() == 0 && !deleted.get()) {
                    deleteStream();
                }
                // Don't call onSendComplete() or drain() - we're done processing
                return;
            }

            long tokenSize = token.length();
            long start = offset.getAndAdd(tokenSize);
            String id = (start + 1) + "-0";

            pendingOperations.incrementAndGet();
            stream.xadd(streamKey, new XAddArgs().id(id), Map.of(TOKEN_FIELD, token))
                    .subscribe()
                    .with(
                            ignored -> {
                                long remaining = pendingOperations.decrementAndGet();
                                onSendComplete();
                                // If completed and this was the last operation, delete the stream
                                if (completed.get() && remaining == 0 && !deleted.get()) {
                                    deleteStream();
                                }
                            },
                            failure -> {
                                long remaining = pendingOperations.decrementAndGet();
                                onSendComplete();
                                // If completed and this was the last operation, delete the stream
                                if (completed.get() && remaining == 0 && !deleted.get()) {
                                    deleteStream();
                                }
                            });
        }

        private void onSendComplete() {
            sending.set(false);
            // Only drain if we're not completed - once completed, we only process the sentinel
            if (!completed.get()) {
                drain();
            } else if (pendingOperations.get() == 0 && !deleted.get()) {
                // If we're completed and all operations are done, delete the stream
                deleteStream();
            }
        }

        @Override
        public void complete() {
            completed.set(true);
            // Queue the completion sentinel - it will be processed after all tokens are sent
            queue.offer(COMPLETE_SENTINEL);
            drain();
        }

        private void deleteStream() {
            if (!deleted.compareAndSet(false, true)) {
                return;
            }
            if (keys == null) {
                LOG.warnf("Keys is null, cannot delete stream for streamKey=%s", streamKey);
                deleted.set(false); // Reset if we can't delete
                return;
            }
            keys.del(streamKey)
                    .subscribe()
                    .with(
                            ignored -> {},
                            failure -> {
                                LOG.warnf(
                                        failure,
                                        "Failed to delete redis stream %s after completion",
                                        streamKey);
                                deleted.set(false); // Reset on failure so we can retry if needed
                            });
        }
    }

    private static final class NoopResponseRecorder implements ResponseRecorder {
        @Override
        public void record(String token) {
            // No-op
        }

        @Override
        public void complete() {
            // No-op
        }
    }
}
