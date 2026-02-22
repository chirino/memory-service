package memory.service.io.github.chirino.dataencryption;

import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;

public interface DataEncryptionProvider {

    String id();

    byte[] encrypt(byte[] plaintext);

    byte[] decrypt(byte[] ciphertext) throws DecryptionFailedException;

    /**
     * Write an {@link EncryptionHeader} to {@code sink}, then return a stream that encrypts all
     * subsequently written bytes into {@code sink}. The caller must close the returned stream to
     * flush the final GCM authentication tag (or any equivalent cipher finalizer).
     */
    default OutputStream encryptingStream(OutputStream sink) throws IOException {
        throw new UnsupportedOperationException("Provider " + id() + " does not support streaming");
    }

    /**
     * Given an already-read {@link EncryptionHeader}, return a decrypting {@link InputStream} over
     * the remaining bytes in {@code source}. The GCM authentication tag is verified when the stream
     * is fully consumed.
     */
    default InputStream decryptingStream(InputStream source, EncryptionHeader header)
            throws IOException {
        throw new UnsupportedOperationException("Provider " + id() + " does not support streaming");
    }
}
