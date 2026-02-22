package memory.service.io.github.chirino.dataencryption;

import jakarta.enterprise.context.ApplicationScoped;
import java.io.ByteArrayInputStream;
import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;

@ApplicationScoped
public class PlainDataEncryptionProvider implements DataEncryptionProvider {

    private static final String PROVIDER_ID = "plain";

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
            throw new IllegalStateException("Failed to write plain encryption header", e);
        }
        return baos.toByteArray();
    }

    @Override
    public byte[] decrypt(byte[] data) throws DecryptionFailedException {
        try {
            ByteArrayInputStream bais = new ByteArrayInputStream(data);
            EncryptionHeader.read(bais); // consume and discard header
            return bais.readAllBytes();
        } catch (IOException e) {
            throw new DecryptionFailedException(
                    "Invalid or missing MSEH header in plain provider data", e);
        }
    }

    @Override
    public OutputStream encryptingStream(OutputStream sink) throws IOException {
        new EncryptionHeader(1, id(), new byte[0]).write(sink);
        return sink; // passthrough — no actual encryption
    }

    @Override
    public InputStream decryptingStream(InputStream source, EncryptionHeader header) {
        return source; // passthrough — no actual decryption
    }
}
