package io.github.chirino.memory.attachment;

import com.mongodb.client.MongoClient;
import com.mongodb.client.gridfs.GridFSBucket;
import com.mongodb.client.gridfs.GridFSBuckets;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.enterprise.inject.Instance;
import jakarta.inject.Inject;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.URI;
import java.nio.file.Files;
import java.nio.file.Path;
import java.sql.Connection;
import java.time.Duration;
import java.util.Optional;
import javax.sql.DataSource;
import org.bson.types.ObjectId;
import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.jboss.logging.Logger;
import org.postgresql.PGConnection;
import org.postgresql.largeobject.LargeObject;
import org.postgresql.largeobject.LargeObjectManager;

/**
 * Database-backed FileStore implementation.
 *
 * <ul>
 *   <li><b>PostgreSQL</b>: Uses the pgjdbc LargeObject API for true streaming. Uploads buffer to a
 *       temp file first (enforcing maxSize), then stream into a LargeObject in a short transaction.
 *       Downloads spool from LargeObject to a temp file in a background thread, with a concurrent
 *       InputStream that reads from the file as it is being written — the JDBC connection is
 *       released as soon as the spool completes, not held for the entire HTTP response.
 *   <li><b>MongoDB</b>: Uses GridFS for chunked streaming. No temp file buffering needed — GridFS
 *       cursors are lightweight and don't hold transactions or locks.
 * </ul>
 */
@ApplicationScoped
public class DatabaseFileStore implements FileStore {

    private static final Logger LOG = Logger.getLogger(DatabaseFileStore.class);

    @ConfigProperty(name = "memory-service.datastore.type", defaultValue = "postgres")
    String datastoreType;

    @Inject Instance<DataSource> dataSource;

    @Inject MongoClient mongoClient;

    @ConfigProperty(name = "quarkus.mongodb.database", defaultValue = "memory_service")
    String mongoDatabaseName;

    private boolean isMongo() {
        String type = datastoreType == null ? "postgres" : datastoreType.trim().toLowerCase();
        return "mongo".equals(type) || "mongodb".equals(type);
    }

    private GridFSBucket gridFSBucket() {
        return GridFSBuckets.create(mongoClient.getDatabase(mongoDatabaseName));
    }

    // ── FileStore interface ──────────────────────────────────────────────

    @Override
    public FileStoreResult store(InputStream data, long maxSize, String contentType)
            throws FileStoreException {
        return isMongo() ? mongoStore(data, maxSize) : postgresStore(data, maxSize);
    }

    @Override
    public InputStream retrieve(String storageKey) throws FileStoreException {
        return isMongo() ? mongoRetrieve(storageKey) : postgresRetrieve(storageKey);
    }

    @Override
    public void delete(String storageKey) {
        if (storageKey == null) return;
        try {
            if (isMongo()) {
                mongoDelete(storageKey);
            } else {
                postgresDelete(storageKey);
            }
        } catch (Exception e) {
            LOG.warnf("Failed to delete blob %s: %s", storageKey, e.getMessage());
        }
    }

    @Override
    public Optional<URI> getSignedUrl(String storageKey, Duration expiry) {
        return Optional.empty();
    }

    // ── PostgreSQL: LargeObject API + temp file buffering ────────────────

    private FileStoreResult postgresStore(InputStream data, long maxSize)
            throws FileStoreException {
        Path tempFile = null;
        try {
            // 1. Buffer upload to temp file, enforcing maxSize via CountingInputStream.
            tempFile = Files.createTempFile("attachment-upload-", ".tmp");
            CountingInputStream counted = new CountingInputStream(data, maxSize);
            try (OutputStream out = Files.newOutputStream(tempFile)) {
                counted.transferTo(out);
            }
            long size = counted.getCount();

            // 2. Short transaction: stream temp file → LargeObject.
            try (Connection conn = dataSource.get().getConnection()) {
                conn.setAutoCommit(false);
                try {
                    LargeObjectManager lom = conn.unwrap(PGConnection.class).getLargeObjectAPI();
                    long oid = lom.createLO(LargeObjectManager.READ | LargeObjectManager.WRITE);
                    LargeObject lo = lom.open(oid, LargeObjectManager.WRITE);
                    try (InputStream in = Files.newInputStream(tempFile)) {
                        byte[] buf = new byte[8192];
                        int n;
                        while ((n = in.read(buf)) != -1) {
                            lo.write(buf, 0, n);
                        }
                    }
                    lo.close();
                    conn.commit();
                    return new FileStoreResult(String.valueOf(oid), size);
                } catch (Exception e) {
                    conn.rollback();
                    throw e;
                }
            }
        } catch (FileStoreException e) {
            throw e;
        } catch (Exception e) {
            throw FileStoreException.storageError("Failed to store file", e);
        } finally {
            deleteTempFile(tempFile);
        }
    }

    private InputStream postgresRetrieve(String storageKey) throws FileStoreException {
        long oid;
        try {
            oid = Long.parseLong(storageKey);
        } catch (NumberFormatException e) {
            throw FileStoreException.storageError("Invalid storage key: " + storageKey, e);
        }

        // Synchronously open the LargeObject so "not found" errors surface immediately.
        Connection conn;
        LargeObject lo;
        try {
            conn = dataSource.get().getConnection();
            conn.setAutoCommit(false);
            LargeObjectManager lom = conn.unwrap(PGConnection.class).getLargeObjectAPI();
            lo = lom.open(oid, LargeObjectManager.READ);
        } catch (Exception e) {
            throw FileStoreException.storageError("Blob not found: " + storageKey, e);
        }

        // Spool LargeObject → temp file in background; return concurrent InputStream.
        // The JDBC connection is released when the background spool finishes, not when
        // the HTTP client finishes downloading.
        try {
            return TempFileSpool.spool(
                    out -> {
                        try {
                            byte[] buf = new byte[8192];
                            int n;
                            while ((n = lo.read(buf, 0, buf.length)) > 0) {
                                out.write(buf, 0, n);
                            }
                        } finally {
                            lo.close();
                            conn.commit();
                            conn.close();
                        }
                    });
        } catch (IOException e) {
            try {
                lo.close();
                conn.rollback();
                conn.close();
            } catch (Exception ignored) {
            }
            throw FileStoreException.storageError("Failed to retrieve blob: " + storageKey, e);
        }
    }

    private void postgresDelete(String storageKey) throws Exception {
        long oid = Long.parseLong(storageKey);
        try (Connection conn = dataSource.get().getConnection()) {
            conn.setAutoCommit(false);
            try {
                LargeObjectManager lom = conn.unwrap(PGConnection.class).getLargeObjectAPI();
                lom.delete(oid);
                conn.commit();
            } catch (Exception e) {
                conn.rollback();
                throw e;
            }
        }
    }

    // ── MongoDB: GridFS ──────────────────────────────────────────────────

    private FileStoreResult mongoStore(InputStream data, long maxSize) throws FileStoreException {
        try {
            CountingInputStream counted = new CountingInputStream(data, maxSize);
            ObjectId fileId = gridFSBucket().uploadFromStream("attachment", counted);
            return new FileStoreResult(fileId.toHexString(), counted.getCount());
        } catch (FileStoreException e) {
            throw e;
        } catch (Exception e) {
            throw FileStoreException.storageError("Failed to store file in GridFS", e);
        }
    }

    private InputStream mongoRetrieve(String storageKey) throws FileStoreException {
        try {
            return gridFSBucket().openDownloadStream(new ObjectId(storageKey));
        } catch (Exception e) {
            throw FileStoreException.storageError("Blob not found: " + storageKey, e);
        }
    }

    private void mongoDelete(String storageKey) {
        gridFSBucket().delete(new ObjectId(storageKey));
    }

    // ── Helpers ──────────────────────────────────────────────────────────

    private static void deleteTempFile(Path path) {
        if (path != null) {
            try {
                Files.deleteIfExists(path);
            } catch (IOException ignored) {
            }
        }
    }
}
