package memory.service.io.github.chirino.dataencryption.dek;

import static org.junit.jupiter.api.Assertions.assertArrayEquals;

import java.lang.reflect.Field;
import java.lang.reflect.Method;
import java.security.SecureRandom;
import java.util.Base64;
import java.util.List;
import java.util.Optional;
import org.junit.jupiter.api.Test;

public class DekMultipleKeysTest {

    @Test
    void decryptsWithLegacyAndCurrentKeys() throws Exception {
        byte[] legacyKey = new byte[32];
        byte[] currentKey = new byte[32];
        SecureRandom random = new SecureRandom();
        random.nextBytes(legacyKey);
        random.nextBytes(currentKey);

        // Provider used to encrypt with legacy key.
        DekDataEncryptionProvider encryptProvider = new DekDataEncryptionProvider();
        setField(encryptProvider, "dekKey", legacyKey);
        invokeInit(encryptProvider);

        byte[] plaintext = "multi-key-rotation".getBytes();
        byte[] ciphertext = encryptProvider.encrypt(plaintext);

        // Provider configured with a new primary key but legacy key as an additional decryption
        // key.
        DekDataEncryptionProvider decryptProvider = new DekDataEncryptionProvider();
        setField(decryptProvider, "dekKey", currentKey);

        List<String> additional = List.of(Base64.getEncoder().encodeToString(legacyKey));
        setField(decryptProvider, "additionalDecryptionKeys", Optional.of(additional));
        invokeInit(decryptProvider);

        byte[] decrypted = decryptProvider.decrypt(ciphertext);
        assertArrayEquals(plaintext, decrypted);
    }

    private void setField(Object target, String name, Object value) throws Exception {
        Field f = target.getClass().getDeclaredField(name);
        f.setAccessible(true);
        f.set(target, value);
    }

    private void invokeInit(DekDataEncryptionProvider provider) throws Exception {
        Method m = DekDataEncryptionProvider.class.getDeclaredMethod("init");
        m.setAccessible(true);
        m.invoke(provider);
    }
}
