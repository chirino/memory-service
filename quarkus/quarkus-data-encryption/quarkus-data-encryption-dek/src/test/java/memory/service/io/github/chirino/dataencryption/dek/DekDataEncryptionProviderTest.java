package memory.service.io.github.chirino.dataencryption.dek;

import static org.junit.jupiter.api.Assertions.assertArrayEquals;
import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNotEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;

import java.io.ByteArrayInputStream;
import java.io.ByteArrayOutputStream;
import java.io.InputStream;
import java.io.OutputStream;
import java.lang.reflect.Field;
import java.nio.ByteBuffer;
import java.security.SecureRandom;
import java.util.Arrays;
import memory.service.io.github.chirino.dataencryption.DecryptionFailedException;
import memory.service.io.github.chirino.dataencryption.EncryptionHeader;
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
    void encryptedBytesStartWithMsehMagic() throws Exception {
        byte[] key = new byte[32];
        new SecureRandom().nextBytes(key);

        DekDataEncryptionProvider provider = createProviderWithKey(key);

        byte[] ciphertext = provider.encrypt("magic-check".getBytes());

        int magic = ByteBuffer.wrap(ciphertext, 0, 4).getInt();
        assertEquals(EncryptionHeader.MAGIC, magic);
    }

    @Test
    void decryptInvalidCiphertextFailsFast() throws Exception {
        byte[] key = new byte[32];
        new SecureRandom().nextBytes(key);

        DekDataEncryptionProvider provider = createProviderWithKey(key);

        byte[] invalid = new byte[] {1, 2, 3};
        assertThrows(DecryptionFailedException.class, () -> provider.decrypt(invalid));
    }

    @Test
    void streamingEncryptDecryptRoundTrip() throws Exception {
        byte[] key = new byte[32];
        new SecureRandom().nextBytes(key);

        DekDataEncryptionProvider provider = createProviderWithKey(key);

        byte[] plaintext = "streaming round trip".getBytes();

        // Encrypt via stream
        ByteArrayOutputStream baos = new ByteArrayOutputStream();
        try (OutputStream encStream = provider.encryptingStream(baos)) {
            encStream.write(plaintext);
        }
        byte[] ciphertext = baos.toByteArray();

        // Decrypt via stream
        ByteArrayInputStream bais = new ByteArrayInputStream(ciphertext);
        EncryptionHeader header = EncryptionHeader.read(bais);
        try (InputStream decStream = provider.decryptingStream(bais, header)) {
            byte[] decrypted = decStream.readAllBytes();
            assertArrayEquals(plaintext, decrypted);
        }
    }

    @Test
    void byteArrayAndStreamFormatsAreCompatible() throws Exception {
        byte[] key = new byte[32];
        new SecureRandom().nextBytes(key);

        DekDataEncryptionProvider provider = createProviderWithKey(key);

        byte[] plaintext = "cross-format".getBytes();

        // Encrypt via byte[] API; decrypt via stream API
        byte[] ciphertext = provider.encrypt(plaintext);

        ByteArrayInputStream bais = new ByteArrayInputStream(ciphertext);
        EncryptionHeader header = EncryptionHeader.read(bais);
        byte[] decrypted;
        try (InputStream decStream = provider.decryptingStream(bais, header)) {
            decrypted = decStream.readAllBytes();
        }
        assertArrayEquals(plaintext, decrypted);
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
