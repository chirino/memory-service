package io.github.chirino.memory.deployment;

import io.quarkus.arc.deployment.AdditionalBeanBuildItem;
import io.quarkus.deployment.annotations.BuildProducer;
import io.quarkus.deployment.annotations.BuildStep;
import io.quarkus.deployment.builditem.AdditionalIndexedClassesBuildItem;
import org.eclipse.microprofile.config.ConfigProvider;

public class ChatMemoryProcessor {

    private static final String CHAT_MEMORY_ENABLED = "memory-service.chat-memory.enabled";

    @BuildStep
    void registerBeans(BuildProducer<AdditionalBeanBuildItem> additionalBeans) {
        if (chatMemoryEnabled()) {
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
        }
        // Always register the factory and supplier so MemoryService can be used as a
        // chatMemoryProviderSupplier even when the global store is disabled.
        additionalBeans.produce(
                AdditionalBeanBuildItem.builder()
                        .setUnremovable()
                        .addBeanClasses(
                                "io.github.chirino.memory.langchain4j.MemoryServiceChatMemoryStoreFactory",
                                "io.github.chirino.memory.langchain4j.MemoryService")
                        .build());
    }

    @BuildStep
    AdditionalIndexedClassesBuildItem indexSupplierClasses() {
        return new AdditionalIndexedClassesBuildItem(
                "io.github.chirino.memory.langchain4j.MemoryService");
    }

    private static boolean chatMemoryEnabled() {
        try {
            return ConfigProvider.getConfig()
                    .getOptionalValue(CHAT_MEMORY_ENABLED, Boolean.class)
                    .orElse(true);
        } catch (IllegalStateException e) {
            return true;
        }
    }
}
