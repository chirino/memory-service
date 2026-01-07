package io.github.chirino.memory.conversation;

import io.quarkus.arc.deployment.AdditionalBeanBuildItem;
import io.quarkus.deployment.annotations.BuildProducer;
import io.quarkus.deployment.annotations.BuildStep;

public class ConversationProcessor {

    @BuildStep
    void registerBeans(BuildProducer<AdditionalBeanBuildItem> beans) {
        beans.produce(
                AdditionalBeanBuildItem.unremovableOf(
                        "io.github.chirino.memory.conversation.runtime.ConversationInterceptor"));
        beans.produce(
                AdditionalBeanBuildItem.unremovableOf(
                        "io.github.chirino.memory.conversation.runtime.DefaultConversationStore"));
    }
}
