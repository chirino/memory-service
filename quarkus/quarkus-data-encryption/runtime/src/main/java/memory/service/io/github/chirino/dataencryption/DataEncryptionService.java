package memory.service.io.github.chirino.dataencryption;

import com.google.protobuf.InvalidProtocolBufferException;
import jakarta.annotation.PostConstruct;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.enterprise.inject.Instance;
import jakarta.inject.Inject;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import org.eclipse.microprofile.config.Config;
import org.eclipse.microprofile.config.ConfigProvider;
import org.eclipse.microprofile.config.inject.ConfigProperty;

@ApplicationScoped
public class DataEncryptionService {

    private final List<String> providerOrder;

    private final Map<String, DataEncryptionProvider> providersById;
    private DataEncryptionProvider primaryProvider;
    private String primaryProviderId;

    @Inject
    public DataEncryptionService(
            @ConfigProperty(name = "data.encryption.providers")
                    Optional<List<String>> providerOrder,
            Instance<DataEncryptionProvider> providersInstance) {
        this.providerOrder = providerOrder.orElse(List.of("plain"));

        Map<String, DataEncryptionProvider> providersByType = new java.util.HashMap<>();
        for (DataEncryptionProvider provider : providersInstance) {
            providersByType.put(provider.id(), provider);
        }

        Config cfg = ConfigProvider.getConfig();
        Map<String, DataEncryptionProvider> byId = new java.util.LinkedHashMap<>();
        for (String id : this.providerOrder) {
            String base = "data.encryption.provider." + id + ".";
            String type = cfg.getOptionalValue(base + "type", String.class).orElse(id);
            boolean enabled = cfg.getOptionalValue(base + "enabled", Boolean.class).orElse(true);
            if (!enabled) {
                continue;
            }
            DataEncryptionProvider provider = providersByType.get(type);
            if (provider != null) {
                byId.put(id, provider);
            }
        }
        // Backwards compatibility: also register providers by their implementation id/type
        // so existing envelopes that used "plain", "dek", etc. as provider ids continue to decrypt.
        for (DataEncryptionProvider provider : providersByType.values()) {
            String typeKey = provider.id();
            byId.putIfAbsent(typeKey, provider);
        }
        this.providersById = byId;
    }

    /**
     * Testing-friendly constructor used by unit tests in the DEK module.
     * This avoids the need for CDI/bootstrap when exercising the service logic.
     */
    public DataEncryptionService(
            DataEncryptionConfig config, List<DataEncryptionProvider> providers) {
        this.providerOrder = config.providers();
        Map<String, DataEncryptionProvider> providersByType = new java.util.HashMap<>();
        for (DataEncryptionProvider provider : providers) {
            providersByType.put(provider.id(), provider);
        }

        Map<String, DataEncryptionProvider> byId = new java.util.LinkedHashMap<>();
        for (String id : config.providers()) {
            DataEncryptionConfig.ProviderConfig cfg = config.provider().get(id);
            String type = cfg != null ? cfg.type() : id;
            boolean enabled = cfg == null || cfg.enabled();
            if (!enabled) {
                continue;
            }
            DataEncryptionProvider provider = providersByType.get(type);
            if (provider != null) {
                byId.put(id, provider);
            }
        }
        // Backwards compatibility: also register providers by their implementation id/type
        // so existing envelopes that used "plain", "dek", etc. as provider ids continue to decrypt.
        for (DataEncryptionProvider provider : providersByType.values()) {
            String typeKey = provider.id();
            byId.putIfAbsent(typeKey, provider);
        }
        this.providersById = byId;
    }

    @PostConstruct
    void init() {
        Optional<Map.Entry<String, DataEncryptionProvider>> first =
                providerOrder.stream()
                        .map(id -> Map.entry(id, providersById.get(id)))
                        .filter(e -> e.getValue() != null)
                        .findFirst();

        Map.Entry<String, DataEncryptionProvider> primary =
                first.orElseThrow(
                        () ->
                                new IllegalStateException(
                                        "No configured data encryption providers are available"));
        this.primaryProviderId = primary.getKey();
        this.primaryProvider = primary.getValue();
    }

    public byte[] encrypt(byte[] plaintext) {
        byte[] ciphertext = primaryProvider.encrypt(plaintext);
        EncryptionEnvelope envelope = EncryptionEnvelope.wrap(primaryProviderId, ciphertext);
        return envelope.toBytes();
    }

    public byte[] decrypt(byte[] data) {
        EncryptionEnvelope envelope;
        try {
            envelope = EncryptionEnvelope.fromBytes(data);
        } catch (InvalidProtocolBufferException e) {
            // Legacy/plain data path: try providers in configured order for fallback.
            for (String id : providerOrder) {
                DataEncryptionProvider provider = providersById.get(id);
                if (provider == null) {
                    continue;
                }
                try {
                    return provider.decrypt(data);
                } catch (DecryptionFailedException ignored) {
                    // Try next provider.
                }
            }
            // No provider could decrypt; treat as plain legacy data.
            return data;
        }

        DataEncryptionProvider provider = providersById.get(envelope.getProviderId());
        if (provider == null) {
            throw new DecryptionFailedException(
                    "No data encryption provider registered with id: " + envelope.getProviderId());
        }
        return provider.decrypt(envelope.getPayload());
    }
}
