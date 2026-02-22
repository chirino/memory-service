package memory.service.io.github.chirino.dataencryption.vault;

import io.quarkus.vault.VaultTransitSecretEngine;
import io.quarkus.vault.transit.ClearData;
import io.quarkus.vault.transit.VaultTransitDataKey;
import io.quarkus.vault.transit.VaultTransitDataKeyRequestDetail;
import io.quarkus.vault.transit.VaultTransitDataKeyType;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import java.nio.charset.StandardCharsets;
import java.util.Base64;
import memory.service.io.github.chirino.dataencryption.GeneratedDek;
import memory.service.io.github.chirino.dataencryption.KeyEncryptionService;
import org.eclipse.microprofile.config.inject.ConfigProperty;

/**
 * Vault-based Key Encryption Service using the Transit secrets engine.
 *
 * <p>
 * This implementation relies on the Quarkiverse Vault extension
 * ({@code io.quarkiverse.vault:quarkus-vault}) and the standard Quarkus Vault
 * configuration properties ({@code quarkus.vault.*}) for connecting to Vault.
 *
 * <p>
 * Only the transit key name is configured here:
 *
 * <pre>
 * memory-service.encryption.vault.transit-key=app-data
 * </pre>
 *
 * The encrypted DEK returned from Vault ({@code ciphertext}) is stored as UTF-8
 * bytes in {@link GeneratedDek#encryptedDek()}.
 */
@ApplicationScoped
public class VaultKeyEncryptionService implements KeyEncryptionService {

    @Inject VaultTransitSecretEngine transit;

    @ConfigProperty(name = "memory-service.encryption.vault.transit-key")
    String transitKey;

    @Override
    public GeneratedDek generateDek() {
        VaultTransitDataKeyRequestDetail detail =
                new VaultTransitDataKeyRequestDetail().setBits(256);

        VaultTransitDataKey dataKey =
                transit.generateDataKey(VaultTransitDataKeyType.wrapped, transitKey, detail);

        // Plaintext is base64-encoded in Vault response.
        byte[] plaintextDek = Base64.getDecoder().decode(dataKey.getPlaintext());
        byte[] encryptedDek = dataKey.getCiphertext().getBytes(StandardCharsets.UTF_8);

        return new GeneratedDek(plaintextDek, encryptedDek);
    }

    @Override
    public byte[] decryptDek(byte[] encryptedDek) {
        String ciphertext = new String(encryptedDek, StandardCharsets.UTF_8);
        ClearData clearData = transit.decrypt(transitKey, ciphertext);
        return clearData.getValue();
    }
}
