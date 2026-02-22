package io.github.chirino.memory.attachment;

import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.io.PipedInputStream;
import java.io.PipedOutputStream;
import java.net.URI;
import java.time.Duration;
import java.util.Map;
import java.util.Optional;
import java.util.concurrent.atomic.AtomicLong;
import java.util.concurrent.atomic.AtomicReference;
import memory.service.io.github.chirino.dataencryption.DataEncryptionService;

/**
 * {@link FileStore} decorator that applies transparent streaming encryption on store and decryption
 * on retrieve. The underlying delegate stores ciphertext; plaintext never touches the storage
 * backend.
 *
 * <p>Encryption uses a virtual-thread pipe so the encryptor and the delegate's {@code store()} run
 * concurrently with no full-file buffering.
 *
 * <p>The {@link FileStoreResult#size()} returned by {@link #store} reflects the plaintext byte
 * count (not the ciphertext size) so that callers (e.g. {@code AttachmentsResource}) can set
 * accurate {@code Content-Length} headers on download responses.
 *
 * <p>{@link #getSignedUrl} always returns empty because stored bytes are ciphertext; a presigned
 * URL would serve garbage to the client.
 */
public class EncryptingFileStore implements FileStore {

    // EncryptionHeader overhead (~30 bytes) + GCM authentication tag (16 bytes), rounded up.
    static final int ENCRYPTION_OVERHEAD = 64;

    private final FileStore delegate;
    private final DataEncryptionService encryption;

    public EncryptingFileStore(FileStore delegate, DataEncryptionService encryption) {
        this.delegate = delegate;
        this.encryption = encryption;
    }

    @Override
    public FileStoreResult store(InputStream data, long maxSize, String contentType)
            throws FileStoreException {
        try {
            PipedInputStream pipedIn = new PipedInputStream(65536);
            PipedOutputStream pipedOut = new PipedOutputStream(pipedIn);
            AtomicReference<Exception> encryptError = new AtomicReference<>();
            // Track plaintext bytes so we can return the correct (plaintext) size.
            AtomicLong plaintextBytes = new AtomicLong(0);

            Thread.ofVirtual()
                    .start(
                            () -> {
                                try (OutputStream encStream =
                                        encryption.encryptingStream(pipedOut)) {
                                    plaintextBytes.set(data.transferTo(encStream));
                                } catch (Exception e) {
                                    encryptError.set(e);
                                } finally {
                                    try {
                                        pipedOut.close();
                                    } catch (IOException ignored) {
                                    }
                                }
                            });

            // Delegate reads ciphertext from the pipe while the virtual thread encrypts.
            // maxSize is widened to accommodate the header prefix and GCM tag.
            FileStoreResult delegateResult =
                    delegate.store(pipedIn, maxSize + ENCRYPTION_OVERHEAD, contentType);

            Exception err = encryptError.get();
            if (err != null) {
                throw new FileStoreException(
                        "encryption_error", 500, err.getMessage(), Map.of(), err);
            }

            // Return plaintext size so Content-Length headers on download are correct.
            return new FileStoreResult(delegateResult.storageKey(), plaintextBytes.get());
        } catch (FileStoreException e) {
            throw e;
        } catch (IOException e) {
            throw FileStoreException.storageError("Failed to encrypt and store data", e);
        }
    }

    @Override
    public InputStream retrieve(String storageKey) throws FileStoreException {
        InputStream cipherStream = delegate.retrieve(storageKey);
        try {
            return encryption.decryptingStream(cipherStream);
        } catch (IOException e) {
            throw FileStoreException.storageError("Failed to decrypt data from storage", e);
        }
    }

    @Override
    public void delete(String storageKey) {
        delegate.delete(storageKey);
    }

    /**
     * Always returns empty. Stored bytes are ciphertext; a presigned URL would serve garbage.
     * Downloads must proxy through the server for decryption.
     */
    @Override
    public Optional<URI> getSignedUrl(String storageKey, Duration expiry) {
        return Optional.empty();
    }
}
