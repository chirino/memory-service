package io.github.chirino.memory.grpc;

import io.github.chirino.memory.security.ApiKeyContext;
import io.github.chirino.memory.security.ApiKeyManager;
import io.grpc.Metadata;
import io.grpc.ServerCall;
import io.grpc.ServerCallHandler;
import io.grpc.ServerInterceptor;
import io.quarkus.grpc.GlobalInterceptor;
import jakarta.inject.Inject;
import jakarta.inject.Singleton;
import org.jboss.logging.Logger;

@Singleton
@GlobalInterceptor
public class GrpcApiKeyInterceptor implements ServerInterceptor {

    private static final Logger LOG = Logger.getLogger(GrpcApiKeyInterceptor.class);
    private static final Metadata.Key<String> API_KEY_HEADER =
            Metadata.Key.of("x-api-key", Metadata.ASCII_STRING_MARSHALLER);

    @Inject ApiKeyManager apiKeyManager;

    @Inject ApiKeyContext apiKeyContext;

    @Override
    public <ReqT, RespT> ServerCall.Listener<ReqT> interceptCall(
            ServerCall<ReqT, RespT> call, Metadata headers, ServerCallHandler<ReqT, RespT> next) {
        apiKeyContext.setValid(false);
        apiKeyContext.setApiKey(null);

        String apiKey = headers.get(API_KEY_HEADER);
        if (apiKey != null && !apiKey.isBlank()) {
            apiKey = apiKey.trim();
            if (apiKeyManager.validate(apiKey)) {
                apiKeyContext.setValid(true);
                apiKeyContext.setApiKey(apiKey);
                LOG.infof(
                        "Received valid API key for gRPC call %s",
                        call.getMethodDescriptor().getFullMethodName());
            } else {
                LOG.debugf(
                        "Received invalid API key for gRPC call %s",
                        call.getMethodDescriptor().getFullMethodName());
            }
        }

        return next.startCall(call, headers);
    }
}
