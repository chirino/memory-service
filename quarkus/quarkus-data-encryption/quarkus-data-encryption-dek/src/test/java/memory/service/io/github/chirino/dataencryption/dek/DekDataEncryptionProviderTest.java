package memory.service.io.github.chirino.dataencryption.dek;

import static org.junit.jupiter.api.Assertions.assertArrayEquals;
import static org.junit.jupiter.api.Assertions.assertNotEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;

import java.lang.reflect.Field;
import java.security.SecureRandom;
import java.util.Arrays;
import memory.service.io.github.chirino.dataencryption.DecryptionFailedException;
import org.junit.jupiter.api.Test;

public class DekDataEncryptionProviderTest {

    @Test
    void encryptAndDecryptRoundTrip() throws Exception {
        byte[] key = new byte[32];
        new SecureRandom().nextBytes(key);

        DekDataEncryptionProvider provider = createProviderWithKey(key);

        byte[] plaintext = "hello-dek".getBytes();
        byte[] ciphertext = provider.encrypt(plaintext);

        assertNotEquals(Arrays.toString(plaintext), Arrays.toString(ciphertext));

        byte[] decrypted = provider.decrypt(ciphertext);
        assertArrayEquals(plaintext, decrypted);
    }

    @Test
    void decryptInvalidCiphertextFailsFast() throws Exception {
        byte[] key = new byte[32];
        new SecureRandom().nextBytes(key);

        DekDataEncryptionProvider provider = createProviderWithKey(key);

        byte[] invalid = new byte[] {1, 2, 3};
        assertThrows(DecryptionFailedException.class, () -> provider.decrypt(invalid));
    }

    private DekDataEncryptionProvider createProviderWithKey(byte[] key) throws Exception {
        DekDataEncryptionProvider provider = new DekDataEncryptionProvider();
        Field field = DekDataEncryptionProvider.class.getDeclaredField("dekKey");
        field.setAccessible(true);
        field.set(provider, key);
        provider.init();
        return provider;
    }
}
