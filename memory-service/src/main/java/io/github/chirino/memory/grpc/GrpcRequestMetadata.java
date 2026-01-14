package io.github.chirino.memory.grpc;

import io.grpc.Context;

public final class GrpcRequestMetadata {
    static final Context.Key<GrpcRequestMetadata> CONTEXT_KEY =
            Context.key("memory-service-grpc-metadata");

    private final String authority;
    private final String forwardedHost;
    private final String forwardedPort;
    private final String localAddress;
    private final Integer localPort;

    GrpcRequestMetadata(
            String authority,
            String forwardedHost,
            String forwardedPort,
            String localAddress,
            Integer localPort) {
        this.authority = authority;
        this.forwardedHost = forwardedHost;
        this.forwardedPort = forwardedPort;
        this.localAddress = localAddress;
        this.localPort = localPort;
    }

    public String authority() {
        return authority;
    }

    public String forwardedHost() {
        return forwardedHost;
    }

    public String forwardedPort() {
        return forwardedPort;
    }

    public String localAddress() {
        return localAddress;
    }

    public Integer localPort() {
        return localPort;
    }

    public static GrpcRequestMetadata current() {
        return CONTEXT_KEY.get();
    }
}
