package io.github.chirino.memory.deployment;

import io.quarkus.arc.deployment.AdditionalBeanBuildItem;
import io.quarkus.deployment.annotations.BuildStep;

public class HistoryProcessor {

    @BuildStep
    AdditionalBeanBuildItem registerBeans() {
        return AdditionalBeanBuildItem.builder()
                .setUnremovable()
                .addBeanClasses(
                        "io.github.chirino.memory.history.runtime.ConversationStore",
                        "io.github.chirino.memory.history.runtime.ConversationInterceptor",
                        "io.github.chirino.memory.history.runtime.NoopResponseResumer",
                        "io.github.chirino.memory.history.runtime.GrpcResponseResumer")
                .build();
    }
}
