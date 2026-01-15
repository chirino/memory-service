package io.github.chirino.memory.deployment;

import io.quarkus.arc.deployment.AdditionalBeanBuildItem;
import io.quarkus.deployment.annotations.BuildStep;

public class MemoryServiceApiProcessor {

    @BuildStep
    AdditionalBeanBuildItem registerBeans() {
        return AdditionalBeanBuildItem.builder()
                .setUnremovable()
                .addBeanClasses(
                        "io.github.chirino.memory.runtime.MemoryServiceApiBuilder",
                        "io.github.chirino.memory.runtime.MemoryServiceApiStartupObserver")
                .build();
    }
}
