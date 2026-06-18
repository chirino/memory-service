package io.github.chirino.memory.deployment;

import io.quarkus.arc.deployment.AdditionalBeanBuildItem;
import io.quarkus.deployment.annotations.BuildProducer;
import io.quarkus.deployment.annotations.BuildStep;
import org.eclipse.microprofile.config.ConfigProvider;

public class ChatMemoryProcessor {

    private static final String CHAT_MEMORY_KIND = "memory-service.chat-memory.kind";
    private static final String MEMORY_SERVICE_KIND = "memory-service";
    private static final String IN_MEMORY_KIND = "in-memory";

    @BuildStep
    void registerBeans(BuildProducer<AdditionalBeanBuildItem> additionalBeans) {
        switch (chatMemoryKind()) {
            case MEMORY_SERVICE_KIND ->
                    additionalBeans.produce(
                            AdditionalBeanBuildItem.builder()
                                    .setUnremovable()
                                    .addBeanClasses(
                                            // We can replace MemoryServiceChatMemoryProvider once
                                            // langchain4j picks up:
                                            // https://github.com/langchain4j/langchain4j/pull/4416
                                            "io.github.chirino.memory.langchain4j.MemoryServiceChatMemoryStore"
                                            // "io.github.chirino.memory.langchain4j.MemoryServiceChatMemoryProvider"
                                            )
                                    .build());
            case IN_MEMORY_KIND -> {
                // Quarkiverse LangChain4j provides the in-memory ChatMemoryStore.
            }
            default ->
                    throw new IllegalArgumentException(
                            CHAT_MEMORY_KIND
                                    + " must be either '"
                                    + MEMORY_SERVICE_KIND
                                    + "' or '"
                                    + IN_MEMORY_KIND
                                    + "'");
        }
    }

    private static String chatMemoryKind() {
        try {
            return ConfigProvider.getConfig()
                    .getOptionalValue(CHAT_MEMORY_KIND, String.class)
                    .map(value -> value.trim().toLowerCase())
                    .filter(value -> !value.isEmpty())
                    .orElse(MEMORY_SERVICE_KIND);
        } catch (IllegalStateException e) {
            return MEMORY_SERVICE_KIND;
        }
    }
}
