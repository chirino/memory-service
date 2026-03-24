package io.github.chirino.memory.deployment;

import io.quarkus.arc.deployment.AdditionalBeanBuildItem;
import io.quarkus.deployment.annotations.BuildStep;

public class SubAgentProcessor {

    @BuildStep
    AdditionalBeanBuildItem registerBeans() {
        return AdditionalBeanBuildItem.builder()
                .setUnremovable()
                .addBeanClasses("io.github.chirino.memory.subagent.runtime.SubAgentTaskManager")
                .addBeanClasses(
                        "io.github.chirino.memory.subagent.runtime.SubAgentToolProviderFactory")
                .build();
    }
}
