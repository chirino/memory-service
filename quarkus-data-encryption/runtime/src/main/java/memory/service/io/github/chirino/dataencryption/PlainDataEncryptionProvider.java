package memory.service.io.github.chirino.dataencryption;

import jakarta.enterprise.context.ApplicationScoped;

@ApplicationScoped
public class PlainDataEncryptionProvider implements DataEncryptionProvider {

    private static final String PROVIDER_ID = "plain";

    @Override
    public String id() {
        return PROVIDER_ID;
    }

    @Override
    public byte[] encrypt(byte[] plaintext) {
        return plaintext;
    }

    @Override
    public byte[] decrypt(byte[] ciphertext) throws DecryptionFailedException {
        return ciphertext;
    }
}
