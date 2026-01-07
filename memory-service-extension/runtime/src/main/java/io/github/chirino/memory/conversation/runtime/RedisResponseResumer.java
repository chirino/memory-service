package io.github.chirino.memory.conversation.runtime;

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
import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.jboss.logging.Logger;

@ApplicationScoped
public class RedisResponseResumer implements ResponseResumer {

    private static final Logger LOG = Logger.getLogger(RedisResponseResumer.class);
    private static final String STREAM_KEY_PREFIX = "conversation:response:";
    private static final String TOKEN_FIELD = "t";

    private final boolean redisEnabled;
    private final String clientName;
    private final Instance<ReactiveRedisDataSource> redisSources;

    private volatile ReactiveStreamCommands<String, String, String> stream;
    private volatile ReactiveKeyCommands<String> keys;

    @Inject
    public RedisResponseResumer(
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
        if (!enabled()) {
            return ResponseResumer.noop().recorder(conversationId);
        }
        return new RedisResponseRecorder(stream, keys, streamKey(conversationId));
    }

    @Override
    public Multi<String> replay(String conversationId, long resumePosition) {
        if (!enabled()) {
            return ResponseResumer.noop().replay(conversationId, resumePosition);
        }

        String streamKey = streamKey(conversationId);
        AtomicLong offset = new AtomicLong(Math.max(0L, resumePosition));

        return keys.exists(streamKey)
                .onItem()
                .transformToMulti(
                        exists -> {
                            if (exists != Boolean.TRUE) {
                                LOG.infof(
                                        "Response stream not found for conversationId=%s,"
                                                + " resumePosition=%d. Returning empty replay.",
                                        conversationId, resumePosition);
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

    private static String streamKey(String conversationId) {
        return STREAM_KEY_PREFIX + conversationId;
    }

    private void readFromStream(
            String streamKey, AtomicLong offset, MultiEmitter<? super String> emitter) {
        if (emitter.isCancelled()) {
            return;
        }

        String startId = (offset.get() + 1) + "-0";
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
            String token = queue.poll();
            if (token == null) {
                sending.set(false);
                // If a token arrived while we were flipping the flag, start another drain so
                // nothing is stranded.
                if (!queue.isEmpty()) {
                    drain();
                }
                return;
            }
            long tokenSize = token.length();
            long start = offset.getAndAdd(tokenSize);
            String id = (start + 1) + "-0";

            // LOG.infof(
            //         "Adding token to redis stream for %s at offset=%d (id=%s, tokenSize=%d).",
            //         streamKey, start, id, tokenSize);

            stream.xadd(streamKey, new XAddArgs().id(id), Map.of(TOKEN_FIELD, token))
                    .subscribe()
                    .with(
                            ignored -> onSendComplete(),
                            failure -> {
                                LOG.warnf(
                                        failure,
                                        "Failed to add token to redis stream for %s at offset=%d"
                                                + " (id=%s, tokenSize=%d). This usually means the"
                                                + " local offset is behind the stream head after a"
                                                + " restart.",
                                        streamKey,
                                        start,
                                        id,
                                        tokenSize);
                                onSendComplete();
                            });
        }

        private void onSendComplete() {
            sending.set(false);
            drain();
        }

        @Override
        public void complete() {
            if (keys == null) {
                return;
            }
            keys.del(streamKey)
                    .subscribe()
                    .with(
                            ignored ->
                                    LOG.debugf(
                                            "Deleted redis stream %s after completion", streamKey),
                            failure ->
                                    LOG.warnf(
                                            failure,
                                            "Failed to delete redis stream %s after completion",
                                            streamKey));
        }
    }
}
