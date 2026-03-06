package memory.service.io.github.chirino.dataencryption;

import jakarta.enterprise.context.ApplicationScoped;
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
        return plaintext; // identity
    }

    @Override
    public byte[] decrypt(byte[] data) {
        return data; // identity
    }

    @Override
    public OutputStream encryptingStream(OutputStream sink) {
        return sink; // passthrough, no header
    }

    @Override
    public InputStream decryptingStream(InputStream source, EncryptionHeader header) {
        return source; // passthrough
    }
}
