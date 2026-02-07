package io.github.chirino.memory.attachment;

import io.github.chirino.memory.config.AttachmentConfig;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.io.RandomAccessFile;
import java.net.URI;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Duration;
import java.util.ArrayList;
import java.util.List;
import java.util.Optional;
import java.util.UUID;
import java.util.concurrent.locks.Condition;
import java.util.concurrent.locks.Lock;
import java.util.concurrent.locks.ReentrantLock;
import org.jboss.logging.Logger;
import software.amazon.awssdk.core.sync.RequestBody;
import software.amazon.awssdk.services.s3.S3Client;
import software.amazon.awssdk.services.s3.model.AbortMultipartUploadRequest;
import software.amazon.awssdk.services.s3.model.CompleteMultipartUploadRequest;
import software.amazon.awssdk.services.s3.model.CompletedMultipartUpload;
import software.amazon.awssdk.services.s3.model.CompletedPart;
import software.amazon.awssdk.services.s3.model.CreateMultipartUploadRequest;
import software.amazon.awssdk.services.s3.model.DeleteObjectRequest;
import software.amazon.awssdk.services.s3.model.GetObjectRequest;
import software.amazon.awssdk.services.s3.model.PutObjectRequest;
import software.amazon.awssdk.services.s3.model.UploadPartRequest;
import software.amazon.awssdk.services.s3.presigner.S3Presigner;
import software.amazon.awssdk.services.s3.presigner.model.GetObjectPresignRequest;

/**
 * S3-backed FileStore implementation.
 *
 * <p>Uploads are buffered to a temp file first (enforcing maxSize via {@link CountingInputStream}),
 * then uploaded to S3 concurrently as the temp file grows:
 *
 * <ul>
 *   <li>Files ≤ 5 MB: simple {@code PutObject} after temp file completes.
 *   <li>Files &gt; 5 MB: S3 multipart upload. 5 MB parts are read from the temp file and uploaded
 *       concurrently while the client upload is still writing to the temp file. This overlaps client
 *       upload with S3 upload, so total latency ≈ max(client, S3) instead of the sum.
 * </ul>
 */
@ApplicationScoped
public class S3FileStore implements FileStore {

    private static final Logger LOG = Logger.getLogger(S3FileStore.class);

    // S3 multipart upload minimum part size is 5 MB (except the last part)
    private static final int MULTIPART_CHUNK_SIZE = 5 * 1024 * 1024;

    @Inject S3Client s3Client;

    @Inject S3Presigner s3Presigner;

    @Inject AttachmentConfig config;

    private String key(String storageKey) {
        String prefix = config.getS3Prefix();
        if (prefix != null && !prefix.isEmpty()) {
            return prefix.endsWith("/") ? prefix + storageKey : prefix + "/" + storageKey;
        }
        return storageKey;
    }

    @Override
    public FileStoreResult store(InputStream data, long maxSize, String contentType)
            throws FileStoreException {
        String storageKey = UUID.randomUUID().toString();
        String s3Key = key(storageKey);
        String bucket = config.getS3Bucket();
        Path tempFile = null;

        try {
            // 1. Start buffering upload to temp file in a background virtual thread.
            //    CountingInputStream enforces maxSize; WriteProgress tracks bytes
            //    so we can start uploading to S3 concurrently.
            tempFile = Files.createTempFile("s3-upload-", ".tmp");
            tempFile.toFile().deleteOnExit();
            WriteProgress progress = new WriteProgress();
            Path tf = tempFile;

            Thread.startVirtualThread(
                    () -> {
                        try (OutputStream out = Files.newOutputStream(tf)) {
                            CountingInputStream counted = new CountingInputStream(data, maxSize);
                            byte[] buf = new byte[8192];
                            int n;
                            while ((n = counted.read(buf)) != -1) {
                                out.write(buf, 0, n);
                                out.flush();
                                progress.addBytes(n);
                            }
                        } catch (Exception e) {
                            progress.setError(e);
                        } finally {
                            progress.setDone();
                        }
                    });

            // 2. Wait to see if file is small (≤ 5MB) or needs multipart.
            progress.awaitBytesOrDone(MULTIPART_CHUNK_SIZE + 1);
            progress.checkError();

            if (progress.isDone() && progress.getBytesWritten() <= MULTIPART_CHUNK_SIZE) {
                // Small file: simple PutObject from temp file.
                long size = progress.getBytesWritten();
                try (InputStream in = Files.newInputStream(tempFile)) {
                    s3Client.putObject(
                            PutObjectRequest.builder()
                                    .bucket(bucket)
                                    .key(s3Key)
                                    .contentLength(size)
                                    .contentType(contentType)
                                    .build(),
                            RequestBody.fromInputStream(in, size));
                }
                return new FileStoreResult(storageKey, size);
            }

            // 3. Large file: multipart upload, reading 5MB chunks from temp file
            //    concurrently as they become available.
            return multipartUploadFromTempFile(
                    tempFile, progress, storageKey, s3Key, bucket, contentType);

        } catch (FileStoreException e) {
            throw e;
        } catch (Exception e) {
            throw FileStoreException.storageError("Failed to store to S3", e);
        } finally {
            deleteTempFile(tempFile);
        }
    }

    private FileStoreResult multipartUploadFromTempFile(
            Path tempFile,
            WriteProgress progress,
            String storageKey,
            String s3Key,
            String bucket,
            String contentType)
            throws Exception {

        String uploadId =
                s3Client.createMultipartUpload(
                                CreateMultipartUploadRequest.builder()
                                        .bucket(bucket)
                                        .key(s3Key)
                                        .contentType(contentType)
                                        .build())
                        .uploadId();

        List<CompletedPart> parts = new ArrayList<>();
        int partNumber = 1;
        long readPosition = 0;

        try (RandomAccessFile raf = new RandomAccessFile(tempFile.toFile(), "r")) {
            while (true) {
                // Wait for a full 5MB chunk or for the writer to finish.
                progress.awaitBytesOrDone(readPosition + MULTIPART_CHUNK_SIZE);
                progress.checkError();

                long remaining = progress.getBytesWritten() - readPosition;
                if (remaining <= 0) break;

                // Full chunk, or whatever is left if the writer is done.
                // (S3 requires non-last parts ≥ 5MB; the await guarantees this.)
                int chunkSize = (int) Math.min(remaining, MULTIPART_CHUNK_SIZE);

                byte[] chunk = new byte[chunkSize];
                raf.seek(readPosition);
                raf.readFully(chunk);
                readPosition += chunkSize;

                parts.add(uploadPart(bucket, s3Key, uploadId, partNumber++, chunk));
            }

            s3Client.completeMultipartUpload(
                    CompleteMultipartUploadRequest.builder()
                            .bucket(bucket)
                            .key(s3Key)
                            .uploadId(uploadId)
                            .multipartUpload(
                                    CompletedMultipartUpload.builder().parts(parts).build())
                            .build());

            return new FileStoreResult(storageKey, progress.getBytesWritten());

        } catch (Exception e) {
            try {
                s3Client.abortMultipartUpload(
                        AbortMultipartUploadRequest.builder()
                                .bucket(bucket)
                                .key(s3Key)
                                .uploadId(uploadId)
                                .build());
            } catch (Exception abortEx) {
                LOG.warnf(
                        "Failed to abort multipart upload %s: %s", uploadId, abortEx.getMessage());
            }
            if (e instanceof FileStoreException fse) throw fse;
            throw FileStoreException.storageError("Failed to store to S3 (multipart)", e);
        }
    }

    private CompletedPart uploadPart(
            String bucket, String key, String uploadId, int partNumber, byte[] data) {
        var response =
                s3Client.uploadPart(
                        UploadPartRequest.builder()
                                .bucket(bucket)
                                .key(key)
                                .uploadId(uploadId)
                                .partNumber(partNumber)
                                .contentLength((long) data.length)
                                .build(),
                        RequestBody.fromBytes(data));
        return CompletedPart.builder().partNumber(partNumber).eTag(response.eTag()).build();
    }

    @Override
    public InputStream retrieve(String storageKey) throws FileStoreException {
        try {
            return s3Client.getObject(
                    GetObjectRequest.builder()
                            .bucket(config.getS3Bucket())
                            .key(key(storageKey))
                            .build());
        } catch (Exception e) {
            throw FileStoreException.storageError("Failed to retrieve from S3: " + storageKey, e);
        }
    }

    @Override
    public void delete(String storageKey) {
        if (storageKey == null) return;
        try {
            s3Client.deleteObject(
                    DeleteObjectRequest.builder()
                            .bucket(config.getS3Bucket())
                            .key(key(storageKey))
                            .build());
        } catch (Exception e) {
            LOG.warnf("Failed to delete S3 object %s: %s", storageKey, e.getMessage());
        }
    }

    @Override
    public Optional<URI> getSignedUrl(String storageKey, Duration expiry) {
        try {
            var presigned =
                    s3Presigner.presignGetObject(
                            GetObjectPresignRequest.builder()
                                    .signatureDuration(expiry)
                                    .getObjectRequest(
                                            GetObjectRequest.builder()
                                                    .bucket(config.getS3Bucket())
                                                    .key(key(storageKey))
                                                    .build())
                                    .build());
            return Optional.of(presigned.url().toURI());
        } catch (Exception e) {
            LOG.warnf("Failed to generate signed URL for %s: %s", storageKey, e.getMessage());
            return Optional.empty();
        }
    }

    // ── Write progress tracking for concurrent temp-file-based uploads ───

    /** Tracks bytes written to the temp file so the S3 uploader can read concurrently. */
    private static final class WriteProgress {

        private final Lock lock = new ReentrantLock();
        private final Condition changed = lock.newCondition();
        private volatile long bytesWritten;
        private volatile boolean done;
        private volatile Throwable error;

        void addBytes(long n) {
            lock.lock();
            try {
                bytesWritten += n;
                changed.signalAll();
            } finally {
                lock.unlock();
            }
        }

        void setDone() {
            lock.lock();
            try {
                done = true;
                changed.signalAll();
            } finally {
                lock.unlock();
            }
        }

        void setError(Throwable e) {
            lock.lock();
            try {
                error = e;
                done = true;
                changed.signalAll();
            } finally {
                lock.unlock();
            }
        }

        long getBytesWritten() {
            return bytesWritten;
        }

        boolean isDone() {
            return done;
        }

        /** Block until at least {@code targetBytes} have been written, or the writer finishes. */
        void awaitBytesOrDone(long targetBytes) throws InterruptedException {
            lock.lock();
            try {
                while (bytesWritten < targetBytes && !done) {
                    changed.await();
                }
            } finally {
                lock.unlock();
            }
        }

        /** Re-throw writer errors, preserving {@link FileStoreException} type. */
        void checkError() {
            Throwable e = error;
            if (e == null) return;
            if (e instanceof FileStoreException fse) throw fse;
            throw FileStoreException.storageError("Upload failed", e);
        }
    }

    private static void deleteTempFile(Path path) {
        if (path != null) {
            try {
                Files.deleteIfExists(path);
            } catch (IOException ignored) {
            }
        }
    }
}
