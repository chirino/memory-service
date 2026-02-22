package memory.service.io.github.chirino.dataencryption;

import jakarta.annotation.PostConstruct;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.enterprise.inject.Instance;
import jakarta.inject.Inject;
import java.io.ByteArrayInputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.io.PushbackInputStream;
import java.nio.ByteBuffer;
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
            @ConfigProperty(name = "memory-service.encryption.providers")
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
            String base = "memory-service.encryption.provider." + id + ".";
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
        // Also register providers by their implementation id/type so MSEH headers that use
        // "plain", "dek", etc. as provider ids continue to route correctly.
        for (DataEncryptionProvider provider : providersByType.values()) {
            String typeKey = provider.id();
            byId.putIfAbsent(typeKey, provider);
        }
        this.providersById = byId;
    }

    /**
     * Testing-friendly constructor used by unit tests in the DEK module. This avoids the need for
     * CDI/bootstrap when exercising the service logic.
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
        // Also register providers by their implementation id/type so MSEH headers that use
        // "plain", "dek", etc. as provider ids continue to route correctly.
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

    /**
     * Returns true when the primary provider performs real encryption (not plain/no-op). Used to
     * automatically derive whether file store encryption should be active.
     */
    public boolean isPrimaryProviderReal() {
        return !(primaryProvider instanceof PlainDataEncryptionProvider);
    }

    public byte[] encrypt(byte[] plaintext) {
        return primaryProvider.encrypt(plaintext);
    }

    public byte[] decrypt(byte[] data) {
        EncryptionHeader header;
        try {
            header = EncryptionHeader.read(new ByteArrayInputStream(data));
        } catch (IOException e) {
            // No valid MSEH header — data was stored without encryption; return as-is.
            return data;
        }
        DataEncryptionProvider provider = providersById.get(header.getProviderId());
        if (provider == null) {
            throw new DecryptionFailedException(
                    "No data encryption provider registered with id: " + header.getProviderId());
        }
        // Provider rereads the header internally from the full byte array to get decryption params.
        return provider.decrypt(data);
    }

    /**
     * Returns an {@link OutputStream} that encrypts into {@code sink}. The provider writes an
     * {@link EncryptionHeader} before the ciphertext. The caller must close the returned stream to
     * flush the final authentication tag.
     */
    public OutputStream encryptingStream(OutputStream sink) throws IOException {
        return primaryProvider.encryptingStream(sink);
    }

    /**
     * Peeks at the first 4 bytes of {@code source}. If the {@link EncryptionHeader#MAGIC} is
     * present, routes to the correct provider for decryption. Otherwise returns the stream
     * unchanged (data was stored without an encryption header).
     */
    public InputStream decryptingStream(InputStream source) throws IOException {
        PushbackInputStream pis = new PushbackInputStream(source, 4);
        byte[] magic = new byte[4];
        int n = pis.read(magic);
        if (n < 4 || ByteBuffer.wrap(magic).getInt() != EncryptionHeader.MAGIC) {
            if (n > 0) pis.unread(magic, 0, n);
            return pis; // no encryption header present — passthrough
        }
        // Valid MSEH prefix; parse remaining header fields and dispatch.
        EncryptionHeader header = EncryptionHeader.readAfterMagic(pis);
        DataEncryptionProvider provider = providersById.get(header.getProviderId());
        if (provider == null) {
            throw new DecryptionFailedException(
                    "No data encryption provider registered with id: " + header.getProviderId());
        }
        return provider.decryptingStream(pis, header);
    }
}
