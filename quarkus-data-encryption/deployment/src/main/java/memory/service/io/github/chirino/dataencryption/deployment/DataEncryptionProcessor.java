package memory.service.io.github.chirino.dataencryption.deployment;

import io.quarkus.arc.deployment.AdditionalBeanBuildItem;
import io.quarkus.deployment.annotations.BuildProducer;
import io.quarkus.deployment.annotations.BuildStep;
import memory.service.io.github.chirino.dataencryption.DataEncryptionService;
import memory.service.io.github.chirino.dataencryption.PlainDataEncryptionProvider;
import memory.service.io.github.chirino.dataencryption.dek.DekDataEncryptionProvider;

public class DataEncryptionProcessor {

    @BuildStep
    void registerBeans(BuildProducer<AdditionalBeanBuildItem> additionalBeans) {
        additionalBeans.produce(AdditionalBeanBuildItem.unremovableOf(DataEncryptionService.class));
        additionalBeans.produce(
                AdditionalBeanBuildItem.unremovableOf(PlainDataEncryptionProvider.class));
        additionalBeans.produce(
                AdditionalBeanBuildItem.unremovableOf(DekDataEncryptionProvider.class));
    }
}
