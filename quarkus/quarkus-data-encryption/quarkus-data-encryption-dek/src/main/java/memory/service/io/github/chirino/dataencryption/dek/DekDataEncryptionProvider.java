package memory.service.io.github.chirino.dataencryption.dek;

import jakarta.annotation.PostConstruct;
import jakarta.enterprise.context.ApplicationScoped;
import java.io.ByteArrayInputStream;
import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.security.GeneralSecurityException;
import java.security.InvalidKeyException;
import java.security.SecureRandom;
import java.util.ArrayList;
import java.util.Base64;
import java.util.List;
import java.util.Optional;
import javax.crypto.Cipher;
import javax.crypto.CipherInputStream;
import javax.crypto.CipherOutputStream;
import javax.crypto.SecretKey;
import javax.crypto.spec.GCMParameterSpec;
import javax.crypto.spec.SecretKeySpec;
import memory.service.io.github.chirino.dataencryption.DataEncryptionProvider;
import memory.service.io.github.chirino.dataencryption.DecryptionFailedException;
import memory.service.io.github.chirino.dataencryption.EncryptionHeader;
import org.eclipse.microprofile.config.Config;
import org.eclipse.microprofile.config.ConfigProvider;
import org.eclipse.microprofile.config.inject.ConfigProperty;

@ApplicationScoped
public class DekDataEncryptionProvider implements DataEncryptionProvider {

    private static final String PROVIDER_ID = "dek";
    private static final int GCM_TAG_LENGTH_BITS = 128;
    private static final int IV_LENGTH_BYTES = 12;

    @ConfigProperty(name = "data.encryption.providers")
    Optional<List<String>> providerOrder;

    // Loaded lazily via ConfigProvider only when the DEK provider is enabled.
    String dekKeyConfig;

    byte[] dekKey;

    /**
     * Optional additional decryption keys, expressed as Base64-encoded strings. The first key
     * (data.encryption.dek.key) is always used for encryption; all keys (primary + additional) are
     * tried for decryption.
     */
    @ConfigProperty(name = "data.encryption.dek.decryption-keys")
    Optional<List<String>> additionalDecryptionKeys;

    private SecretKey secretKey;
    private List<SecretKey> decryptionKeys;
    private final SecureRandom secureRandom = new SecureRandom();

    @PostConstruct
    void init() {
        boolean dekEnabled = true;
        if (providerOrder != null && providerOrder.isPresent()) {
            Config cfg = ConfigProvider.getConfig();
            dekEnabled =
                    providerOrder.get().stream()
                            .anyMatch(
                                    id -> {
                                        String base = "data.encryption.provider." + id + ".";
                                        String type =
                                                cfg.getOptionalValue(base + "type", String.class)
                                                        .orElse(id);
                                        boolean enabled =
                                                cfg.getOptionalValue(
                                                                base + "enabled", Boolean.class)
                                                        .orElse(true);
                                        return enabled && PROVIDER_ID.equals(type);
                                    });
        } else if (providerOrder != null) {
            // Property absent, mirror DataEncryptionService default which prefers "plain".
            dekEnabled = false;
        }

        if (!dekEnabled) {
            // Provider is not configured in data.encryption.providers; skip key initialization.
            this.decryptionKeys = List.of();
            return;
        }

        if (dekKey == null) {
            if (dekKeyConfig == null) {
                Config cfg = ConfigProvider.getConfig();
                dekKeyConfig =
                        cfg.getOptionalValue("data.encryption.dek.key", String.class).orElse("");
            }
            if (dekKeyConfig == null || dekKeyConfig.isBlank()) {
                throw new IllegalStateException(
                        "data.encryption.dek.key must be set to a Base64-encoded 32-byte AES-256"
                                + " key");
            }
            try {
                dekKey = Base64.getDecoder().decode(dekKeyConfig.trim());
            } catch (IllegalArgumentException e) {
                throw new IllegalStateException(
                        "data.encryption.dek.key must be a Base64-encoded 32-byte AES-256 key", e);
            }
        }

        if (dekKey == null || dekKey.length != 32) {
            throw new IllegalStateException(
                    "data.encryption.dek.key must be a 32-byte AES-256 key");
        }
        this.secretKey = new SecretKeySpec(dekKey, "AES");

        List<SecretKey> keys = new ArrayList<>();
        keys.add(this.secretKey);

        if (additionalDecryptionKeys != null && additionalDecryptionKeys.isPresent()) {
            for (String encoded : additionalDecryptionKeys.get()) {
                if (encoded == null || encoded.isBlank()) {
                    continue;
                }
                byte[] keyBytes = Base64.getDecoder().decode(encoded.trim());
                if (keyBytes.length != 32) {
                    throw new IllegalStateException(
                            "Each data.encryption.dek.decryption-keys entry must decode to a"
                                    + " 32-byte AES-256 key");
                }
                keys.add(new SecretKeySpec(keyBytes, "AES"));
            }
        }

        this.decryptionKeys = List.copyOf(keys);
    }

    @Override
    public String id() {
        return PROVIDER_ID;
    }

    @Override
    public byte[] encrypt(byte[] plaintext) {
        ByteArrayOutputStream baos = new ByteArrayOutputStream();
        try (OutputStream out = encryptingStream(baos)) {
            out.write(plaintext);
        } catch (IOException e) {
            throw new IllegalStateException("Failed to encrypt data with DEK provider", e);
        }
        return baos.toByteArray();
    }

    @Override
    public byte[] decrypt(byte[] data) throws DecryptionFailedException {
        try {
            ByteArrayInputStream bais = new ByteArrayInputStream(data);
            EncryptionHeader header = EncryptionHeader.read(bais);
            byte[] ciphertext = bais.readAllBytes();

            GeneralSecurityException lastFailure = null;
            for (SecretKey key : decryptionKeys) {
                try {
                    Cipher cipher = Cipher.getInstance("AES/GCM/NoPadding");
                    cipher.init(
                            Cipher.DECRYPT_MODE,
                            key,
                            new GCMParameterSpec(GCM_TAG_LENGTH_BITS, header.getIv()));
                    return cipher.doFinal(ciphertext);
                } catch (GeneralSecurityException e) {
                    lastFailure = e;
                }
            }

            throw new DecryptionFailedException(
                    "Failed to decrypt data with DEK provider", lastFailure);
        } catch (DecryptionFailedException e) {
            throw e;
        } catch (IOException e) {
            throw new DecryptionFailedException(
                    "Invalid or corrupt MSEH header in DEK-encrypted data", e);
        } catch (RuntimeException e) {
            throw new DecryptionFailedException("Failed to decrypt data with DEK provider", e);
        }
    }

    @Override
    public OutputStream encryptingStream(OutputStream sink) throws IOException {
        byte[] iv = new byte[IV_LENGTH_BYTES];
        secureRandom.nextBytes(iv);
        new EncryptionHeader(1, id(), iv).write(sink);
        try {
            Cipher cipher = Cipher.getInstance("AES/GCM/NoPadding");
            cipher.init(
                    Cipher.ENCRYPT_MODE, secretKey, new GCMParameterSpec(GCM_TAG_LENGTH_BITS, iv));
            return new CipherOutputStream(sink, cipher);
        } catch (GeneralSecurityException e) {
            throw new IOException("Failed to initialize AES/GCM cipher for encryption", e);
        }
    }

    @Override
    public InputStream decryptingStream(InputStream source, EncryptionHeader header)
            throws IOException {
        // Try keys at init time; InvalidKeyException means the key can't be used at all.
        // For wrong-but-valid AES keys, the GCM tag failure surfaces at end-of-stream.
        for (SecretKey key : decryptionKeys) {
            try {
                Cipher cipher = Cipher.getInstance("AES/GCM/NoPadding");
                cipher.init(
                        Cipher.DECRYPT_MODE,
                        key,
                        new GCMParameterSpec(GCM_TAG_LENGTH_BITS, header.getIv()));
                return new CipherInputStream(source, cipher);
            } catch (InvalidKeyException e) {
                // Try next key
            } catch (GeneralSecurityException e) {
                throw new IOException("Failed to initialize AES/GCM cipher for decryption", e);
            }
        }
        throw new IOException("No configured key could initialize AES/GCM cipher for decryption");
    }
}
