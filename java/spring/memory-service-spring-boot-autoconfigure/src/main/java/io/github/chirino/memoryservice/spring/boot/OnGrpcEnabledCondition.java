package io.github.chirino.memoryservice.spring.boot;

import org.springframework.boot.autoconfigure.condition.ConditionMessage;
import org.springframework.boot.autoconfigure.condition.ConditionOutcome;
import org.springframework.boot.autoconfigure.condition.SpringBootCondition;
import org.springframework.context.annotation.ConditionContext;
import org.springframework.core.type.AnnotatedTypeMetadata;
import org.springframework.util.StringUtils;

/**
 * Condition that matches when gRPC should be enabled. gRPC is enabled when:
 * <ul>
 *   <li>{@code memory-service.grpc.enabled=true} is explicitly set, OR</li>
 *   <li>{@code memory-service.client.url} is configured (auto-derive gRPC settings)</li>
 * </ul>
 */
public class OnGrpcEnabledCondition extends SpringBootCondition {

    @Override
    public ConditionOutcome getMatchOutcome(
            ConditionContext context, AnnotatedTypeMetadata metadata) {

        String grpcEnabled = context.getEnvironment().getProperty("memory-service.grpc.enabled");
        String url = context.getEnvironment().getProperty("memory-service.client.url");

        // Check if explicitly enabled
        if ("true".equalsIgnoreCase(grpcEnabled)) {
            return ConditionOutcome.match(
                    ConditionMessage.forCondition("OnGrpcEnabled")
                            .found("property")
                            .items("memory-service.grpc.enabled=true"));
        }

        // Check if explicitly disabled
        if ("false".equalsIgnoreCase(grpcEnabled)) {
            return ConditionOutcome.noMatch(
                    ConditionMessage.forCondition("OnGrpcEnabled")
                            .found("property")
                            .items("memory-service.grpc.enabled=false"));
        }

        // Auto-enable if url is configured
        if (StringUtils.hasText(url)) {
            return ConditionOutcome.match(
                    ConditionMessage.forCondition("OnGrpcEnabled")
                            .found("property")
                            .items("memory-service.client.url=" + url));
        }

        // No configuration found
        return ConditionOutcome.noMatch(
                ConditionMessage.forCondition("OnGrpcEnabled")
                        .didNotFind("property")
                        .items("memory-service.grpc.enabled or memory-service.client.url"));
    }
}
