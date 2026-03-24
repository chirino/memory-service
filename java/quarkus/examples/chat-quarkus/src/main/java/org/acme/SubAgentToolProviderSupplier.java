package org.acme;

import dev.langchain4j.service.tool.ToolProvider;
import io.github.chirino.memory.subagent.runtime.SubAgentToolProviderFactory;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import java.util.List;
import java.util.function.Supplier;

@ApplicationScoped
public class SubAgentToolProviderSupplier implements Supplier<ToolProvider> {

    @Inject SubAgent subAgent;
    @Inject SubAgentToolProviderFactory factory;

    @Override
    public ToolProvider get() {
        return factory.builder()
                .maxConcurrency(3)
                .createStreamingProvider(
                        request ->
                                subAgent.chat(
                                        request.childConversationId(),
                                        request.message(),
                                        List.of()));
    }
}
