package memory.service.io.github.chirino.dataencryption;

import static org.junit.jupiter.api.Assertions.assertArrayEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;

import java.util.Arrays;
import org.junit.jupiter.api.Test;

public class PlainDataEncryptionProviderTest {

    @Test
    void encryptAddsHeaderAndDecryptRestoresPlaintext() throws Exception {
        PlainDataEncryptionProvider provider = new PlainDataEncryptionProvider();
        byte[] data = "plain-text".getBytes();

        byte[] encrypted = provider.encrypt(data);

        // encrypt() now prepends the MSEH header, so bytes differ from plaintext
        assertFalse(Arrays.equals(data, encrypted), "encrypt() should prepend MSEH header");

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
