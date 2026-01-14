package io.github.chirino.memory.grpc;

import io.grpc.Context;
import io.grpc.Contexts;
import io.grpc.Grpc;
import io.grpc.Metadata;
import io.grpc.ServerCall;
import io.grpc.ServerCallHandler;
import io.grpc.ServerInterceptor;
import io.quarkus.grpc.GlobalInterceptor;
import jakarta.inject.Singleton;
import java.net.InetSocketAddress;
import java.net.SocketAddress;

@Singleton
@GlobalInterceptor
public class GrpcRequestMetadataInterceptor implements ServerInterceptor {
    private static final Metadata.Key<String> FORWARDED_HOST =
            Metadata.Key.of("x-forwarded-host", Metadata.ASCII_STRING_MARSHALLER);
    private static final Metadata.Key<String> FORWARDED_PORT =
            Metadata.Key.of("x-forwarded-port", Metadata.ASCII_STRING_MARSHALLER);

    @Override
    public <ReqT, RespT> ServerCall.Listener<ReqT> interceptCall(
            ServerCall<ReqT, RespT> call, Metadata headers, ServerCallHandler<ReqT, RespT> next) {
        String authority = call.getAuthority();
        String forwardedHost = headers.get(FORWARDED_HOST);
        String forwardedPort = headers.get(FORWARDED_PORT);
        String localAddress = null;
        Integer localPort = null;
        SocketAddress socketAddress = call.getAttributes().get(Grpc.TRANSPORT_ATTR_LOCAL_ADDR);
        if (socketAddress instanceof InetSocketAddress inetSocketAddress) {
            if (inetSocketAddress.getAddress() != null) {
                localAddress = inetSocketAddress.getAddress().getHostAddress();
            } else {
                localAddress = inetSocketAddress.getHostString();
            }
            localPort = inetSocketAddress.getPort();
        }

        GrpcRequestMetadata metadata =
                new GrpcRequestMetadata(
                        authority, forwardedHost, forwardedPort, localAddress, localPort);
        Context context = Context.current().withValue(GrpcRequestMetadata.CONTEXT_KEY, metadata);
        return Contexts.interceptCall(context, call, headers, next);
    }
}
