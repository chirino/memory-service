package io.github.chirino.memory.deployment;

import io.quarkus.arc.deployment.AdditionalBeanBuildItem;
import io.quarkus.deployment.annotations.BuildStep;

public class ChatMemoryProcessor {

    @BuildStep
    AdditionalBeanBuildItem registerBeans() {
        return AdditionalBeanBuildItem.builder()
                .setUnremovable()
                .addBeanClasses(
                        // We can replace MemoryServiceChatMemoryProvider once langchain4j picks up:
                        // https://github.com/langchain4j/langchain4j/pull/4416
                        // "io.github.chirino.memory.langchain4j.MemoryServiceChatMemoryStore",
                        "io.github.chirino.memory.langchain4j.MemoryServiceChatMemoryProvider")
                .build();
    }
}
