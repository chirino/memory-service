package memory.service.io.github.chirino.dataencryption;

import static org.junit.jupiter.api.Assertions.assertArrayEquals;

import org.junit.jupiter.api.Test;

public class PlainDataEncryptionProviderTest {

    @Test
    void encryptIsIdentity() throws Exception {
        PlainDataEncryptionProvider provider = new PlainDataEncryptionProvider();
        byte[] data = "plain-text".getBytes();

        byte[] encrypted = provider.encrypt(data);

        // Plain provider is a true no-op: output must equal input
        assertArrayEquals(data, encrypted, "encrypt() should return input unchanged");

        byte[] decrypted = provider.decrypt(encrypted);
        assertArrayEquals(data, decrypted);
    }

    @Test
    void streamingRoundTrip() throws Exception {
        PlainDataEncryptionProvider provider = new PlainDataEncryptionProvider();
        byte[] data = "streaming-plain".getBytes();

        byte[] encrypted = provider.encrypt(data);
        byte[] decrypted = provider.decrypt(encrypted);

        assertArrayEquals(data, decrypted);
    }
}
