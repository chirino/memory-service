package io.github.chirino.memory.deployment;

import io.quarkus.arc.deployment.AdditionalBeanBuildItem;
import io.quarkus.deployment.annotations.BuildStep;

public class Langchain4jProcessor {

    @BuildStep
    AdditionalBeanBuildItem registerBeans() {
        return AdditionalBeanBuildItem.builder()
                .setUnremovable()
                .addBeanClasses(
                        "io.github.chirino.memory.langchain4j.MemoryServiceChatMemoryProvider",
                        "io.github.chirino.memory.langchain4j.RequestContextExecutor")
                .build();
    }
}
