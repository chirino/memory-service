package memory.service.io.github.chirino.dataencryption;

import io.smallrye.config.ConfigMapping;
import io.smallrye.config.WithDefault;
import java.util.List;
import java.util.Map;

/**
 * Root configuration for the data encryption extension.
 *
 * <pre>
 * memory-service.encryption.providers=a,b
 * memory-service.encryption.provider.a.type=vault
 * memory-service.encryption.provider.b.type=plain
 * </pre>
 */
@ConfigMapping(prefix = "memory-service.encryption")
public interface DataEncryptionConfig {

    /**
     * Ordered list of provider ids, first is used for new encrypt operations.
     */
    @WithDefault("plain")
    List<String> providers();

    /**
     * Per-provider configuration, keyed by provider id.
     */
    Map<String, ProviderConfig> provider();

    /**
     * Configuration for a single data encryption provider.
     */
    interface ProviderConfig {

        /**
         * Provider type, for example: dek, vault, aws-kms, plain.
         */
        String type();

        /**
         * Whether this provider should be used for encryption of new data.
         * By default all providers can be used, but the first in the list wins.
         */
        @WithDefault("true")
        boolean enabled();
    }
}
