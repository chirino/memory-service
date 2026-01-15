package io.github.chirino.memory.grpc;

import io.github.chirino.memory.security.ApiKeyContext;
import io.grpc.ForwardingServerCall;
import io.grpc.Grpc;
import io.grpc.Metadata;
import io.grpc.ServerCall;
import io.grpc.ServerCallHandler;
import io.grpc.ServerInterceptor;
import io.quarkus.grpc.GlobalInterceptor;
import io.quarkus.security.identity.SecurityIdentity;
import jakarta.inject.Inject;
import jakarta.inject.Singleton;
import java.net.SocketAddress;
import java.time.Duration;
import org.jboss.logging.Logger;

@Singleton
@GlobalInterceptor
public class GrpcAccessLogInterceptor implements ServerInterceptor {
    private static final Logger LOG = Logger.getLogger(GrpcAccessLogInterceptor.class);

    @Inject SecurityIdentity identity;

    @Inject ApiKeyContext apiKeyContext;

    @Override
    public <ReqT, RespT> ServerCall.Listener<ReqT> interceptCall(
            ServerCall<ReqT, RespT> call, Metadata headers, ServerCallHandler<ReqT, RespT> next) {
        long startedAt = System.nanoTime();
        String method = call.getMethodDescriptor().getFullMethodName();
        SocketAddress remote = call.getAttributes().get(Grpc.TRANSPORT_ATTR_REMOTE_ADDR);
        String remoteValue = remote == null ? "unknown" : remote.toString();

        String principalName =
                identity != null && identity.getPrincipal() != null
                        ? identity.getPrincipal().getName()
                        : "anonymous";
        boolean hasApiKey = apiKeyContext != null && apiKeyContext.hasValidApiKey();
        String clientId =
                apiKeyContext != null && apiKeyContext.hasValidApiKey()
                        ? apiKeyContext.getClientId()
                        : null;

        LOG.infof(
                "gRPC start %s from %s => principal name: %s, has api key: %s, client id: %s",
                method, remoteValue, principalName, hasApiKey, clientId);

        ForwardingServerCall.SimpleForwardingServerCall<ReqT, RespT> wrappedCall =
                new ForwardingServerCall.SimpleForwardingServerCall<>(call) {
                    @Override
                    public void close(io.grpc.Status status, Metadata trailers) {
                        long durationMs =
                                Duration.ofNanos(System.nanoTime() - startedAt).toMillis();
                        LOG.infof(
                                "gRPC end %s status=%s durationMs=%d",
                                method, status.getCode(), durationMs);
                        super.close(status, trailers);
                    }
                };

        return next.startCall(wrappedCall, headers);
    }
}
