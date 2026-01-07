package memory.service.io.github.chirino.dataencryption;

import static org.junit.jupiter.api.Assertions.assertArrayEquals;

import org.junit.jupiter.api.Test;

public class PlainDataEncryptionProviderTest {

    @Test
    void encryptAndDecryptAreIdentity() {
        PlainDataEncryptionProvider provider = new PlainDataEncryptionProvider();
        byte[] data = "plain-text".getBytes();

        byte[] encrypted = provider.encrypt(data);
        byte[] decrypted = provider.decrypt(encrypted);

        assertArrayEquals(data, encrypted);
        assertArrayEquals(data, decrypted);
    }
}
