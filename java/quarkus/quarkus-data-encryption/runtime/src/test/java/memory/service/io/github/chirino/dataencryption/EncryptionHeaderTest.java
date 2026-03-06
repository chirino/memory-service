package memory.service.io.github.chirino.dataencryption;

import static org.junit.jupiter.api.Assertions.assertArrayEquals;
import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertThrows;

import java.io.ByteArrayInputStream;
import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.nio.ByteBuffer;
import org.junit.jupiter.api.Test;

public class EncryptionHeaderTest {

    @Test
    void roundTripWriteRead() throws IOException {
        byte[] iv = new byte[] {1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12};
        EncryptionHeader original = new EncryptionHeader(1, "dek", iv);

        ByteArrayOutputStream baos = new ByteArrayOutputStream();
        original.write(baos);
        byte[] written = baos.toByteArray();

        EncryptionHeader read = EncryptionHeader.read(new ByteArrayInputStream(written));

        assertEquals(1, read.getVersion());
        assertEquals("dek", read.getProviderId());
        assertArrayEquals(iv, read.getIv());
    }

    @Test
    void magicBytesAreFirst4Bytes() throws IOException {
        EncryptionHeader header = new EncryptionHeader(1, "plain", new byte[0]);
        ByteArrayOutputStream baos = new ByteArrayOutputStream();
        header.write(baos);
        byte[] bytes = baos.toByteArray();

        int magic = ByteBuffer.wrap(bytes, 0, 4).getInt();
        assertEquals(EncryptionHeader.MAGIC, magic);
    }

    @Test
    void readThrowsOnWrongMagic() {
        byte[] badMagic = new byte[] {0x00, 0x01, 0x02, 0x03, 0x0A};
        assertThrows(
                IOException.class, () -> EncryptionHeader.read(new ByteArrayInputStream(badMagic)));
    }

    @Test
    void readThrowsOnTruncatedStream() {
        // Only 2 bytes — not enough for magic
        byte[] truncated = new byte[] {0x4D, 0x53};
        assertThrows(
                IOException.class,
                () -> EncryptionHeader.read(new ByteArrayInputStream(truncated)));
    }

    @Test
    void roundTripEmptyIv() throws IOException {
        EncryptionHeader original = new EncryptionHeader(1, "plain", new byte[0]);
        ByteArrayOutputStream baos = new ByteArrayOutputStream();
        original.write(baos);
        EncryptionHeader read = EncryptionHeader.read(new ByteArrayInputStream(baos.toByteArray()));

        assertEquals("plain", read.getProviderId());
        assertArrayEquals(new byte[0], read.getIv());
    }

    @Test
    void readLeavesRemainingBytesIntact() throws IOException {
        byte[] iv = new byte[12];
        EncryptionHeader header = new EncryptionHeader(1, "dek", iv);
        ByteArrayOutputStream baos = new ByteArrayOutputStream();
        header.write(baos);
        byte[] payload = new byte[] {0x11, 0x22, 0x33};
        baos.write(payload);

        ByteArrayInputStream bais = new ByteArrayInputStream(baos.toByteArray());
        EncryptionHeader.read(bais); // consume header
        byte[] remaining = bais.readAllBytes();

        assertArrayEquals(payload, remaining);
    }
}
