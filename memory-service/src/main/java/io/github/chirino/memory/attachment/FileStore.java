package io.github.chirino.memory.attachment;

import java.io.InputStream;
import java.net.URI;
import java.time.Duration;
import java.util.Optional;

public interface FileStore {

    /**
     * Store file data and return the storage key and size. Implementations enforce the maxSize
     * limit and may read the stream in chunks to bound memory usage.
     *
     * @param contentType MIME type of the file (used by S3 to set Content-Type on the object)
     */
    FileStoreResult store(InputStream data, long maxSize, String contentType)
            throws FileStoreException;

    InputStream retrieve(String storageKey) throws FileStoreException;

    void delete(String storageKey);

    Optional<URI> getSignedUrl(String storageKey, Duration expiry);
}
