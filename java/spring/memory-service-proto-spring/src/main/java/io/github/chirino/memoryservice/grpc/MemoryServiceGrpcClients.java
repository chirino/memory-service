package io.github.chirino.memoryservice.grpc;

import io.github.chirino.memory.grpc.v1.ConversationMembershipsServiceGrpc;
import io.github.chirino.memory.grpc.v1.ConversationsServiceGrpc;
import io.github.chirino.memory.grpc.v1.EntriesServiceGrpc;
import io.github.chirino.memory.grpc.v1.ResponseRecorderServiceGrpc;
import io.github.chirino.memory.grpc.v1.SearchServiceGrpc;
import io.github.chirino.memory.grpc.v1.SystemServiceGrpc;
import io.grpc.ManagedChannel;
import io.grpc.ManagedChannelBuilder;
import io.grpc.Metadata;
import io.grpc.netty.shaded.io.grpc.netty.NettyChannelBuilder;
import io.grpc.netty.shaded.io.netty.channel.EventLoopGroup;
import io.grpc.netty.shaded.io.netty.channel.nio.NioEventLoopGroup;
import io.grpc.netty.shaded.io.netty.channel.socket.nio.NioDomainSocketChannel;
import io.grpc.netty.shaded.io.netty.util.concurrent.DefaultThreadFactory;
import io.grpc.stub.MetadataUtils;
import java.net.UnixDomainSocketAddress;
import java.util.Map;
import java.util.concurrent.TimeUnit;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.util.Assert;
import org.springframework.util.StringUtils;

public final class MemoryServiceGrpcClients {

    private static final Logger LOGGER = LoggerFactory.getLogger(MemoryServiceGrpcClients.class);
    private static final EventLoopGroup UDS_EVENT_LOOP_GROUP =
            new NioEventLoopGroup(1, new DefaultThreadFactory("memory-service-grpc-uds", true));

    static {
        Runtime.getRuntime()
                .addShutdownHook(
                        new Thread(
                                () -> UDS_EVENT_LOOP_GROUP.shutdownGracefully(),
                                "memory-service-grpc-uds-shutdown"));
    }

    private MemoryServiceGrpcClients() {}

    public static ManagedChannelBuilder<?> channelBuilder(MemoryServiceGrpcProperties properties) {
        ManagedChannelBuilder<?> builder;
        if (StringUtils.hasText(properties.getUnixSocket())) {
            builder = buildUnixSocketChannel(properties.getUnixSocket());
        } else {
            builder = ManagedChannelBuilder.forTarget(properties.getTarget());
        }
        if (properties.isPlaintext()) {
            builder.usePlaintext();
        }
        if (properties.getKeepAliveTime() != null) {
            builder.keepAliveTime(properties.getKeepAliveTime().toMillis(), TimeUnit.MILLISECONDS);
        }
        if (properties.getKeepAliveTimeout() != null) {
            builder.keepAliveTimeout(
                    properties.getKeepAliveTimeout().toMillis(), TimeUnit.MILLISECONDS);
        }
        if (!properties.getHeaders().isEmpty()) {
            builder.intercept(headerInterceptor(properties.getHeaders()));
        }
        return builder;
    }

    private static ManagedChannelBuilder<?> buildUnixSocketChannel(String unixSocket) {
        String socketPath = unixSocket.trim();
        Assert.isTrue(
                socketPath.startsWith("/"),
                "memory-service.grpc.unix-socket must be an absolute path");
        return NettyChannelBuilder.forAddress(UnixDomainSocketAddress.of(socketPath))
                .channelType(NioDomainSocketChannel.class, UnixDomainSocketAddress.class)
                .eventLoopGroup(UDS_EVENT_LOOP_GROUP);
    }

    public static MemoryServiceStubs stubs(ManagedChannel channel) {
        return new MemoryServiceStubs(channel);
    }

    private static io.grpc.ClientInterceptor headerInterceptor(Map<String, String> headers) {
        Metadata metadata = new Metadata();
        headers.forEach(
                (name, value) ->
                        metadata.put(
                                Metadata.Key.of(name, Metadata.ASCII_STRING_MARSHALLER), value));
        return MetadataUtils.newAttachHeadersInterceptor(metadata);
    }

    public static final class MemoryServiceStubs implements AutoCloseable {

        private final ManagedChannel channel;
        private final SystemServiceGrpc.SystemServiceBlockingStub systemService;
        private final ConversationsServiceGrpc.ConversationsServiceBlockingStub
                conversationsService;
        private final ConversationMembershipsServiceGrpc.ConversationMembershipsServiceBlockingStub
                membershipsService;
        private final EntriesServiceGrpc.EntriesServiceBlockingStub entriesService;
        private final SearchServiceGrpc.SearchServiceBlockingStub searchService;
        private final ResponseRecorderServiceGrpc.ResponseRecorderServiceStub
                responseRecorderService;

        public MemoryServiceStubs(ManagedChannel channel) {
            this.channel = channel;
            this.systemService = SystemServiceGrpc.newBlockingStub(channel);
            this.conversationsService = ConversationsServiceGrpc.newBlockingStub(channel);
            this.membershipsService = ConversationMembershipsServiceGrpc.newBlockingStub(channel);
            this.entriesService = EntriesServiceGrpc.newBlockingStub(channel);
            this.searchService = SearchServiceGrpc.newBlockingStub(channel);
            this.responseRecorderService = ResponseRecorderServiceGrpc.newStub(channel);
        }

        public SystemServiceGrpc.SystemServiceBlockingStub systemService() {
            return systemService;
        }

        public ConversationsServiceGrpc.ConversationsServiceBlockingStub conversationsService() {
            return conversationsService;
        }

        public ConversationMembershipsServiceGrpc.ConversationMembershipsServiceBlockingStub
                membershipsService() {
            return membershipsService;
        }

        public EntriesServiceGrpc.EntriesServiceBlockingStub entriesService() {
            return entriesService;
        }

        public SearchServiceGrpc.SearchServiceBlockingStub searchService() {
            return searchService;
        }

        public ResponseRecorderServiceGrpc.ResponseRecorderServiceStub responseRecorderService() {
            return responseRecorderService;
        }

        @Override
        public void close() {
            channel.shutdown();
            try {
                if (!channel.awaitTermination(5, TimeUnit.SECONDS)) {
                    LOGGER.warn(
                            "memory-service gRPC channel did not terminate cleanly, forcing"
                                    + " shutdown");
                    channel.shutdownNow();
                }
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
                channel.shutdownNow();
            }
        }
    }
}
