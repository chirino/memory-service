package io.github.chirino.memory.grpc;

import com.google.protobuf.ByteString;
import io.github.chirino.memory.attachment.AttachmentDto;
import io.github.chirino.memory.attachment.AttachmentStore;
import io.github.chirino.memory.attachment.AttachmentStoreSelector;
import io.github.chirino.memory.attachment.FileStore;
import io.github.chirino.memory.attachment.FileStoreResult;
import io.github.chirino.memory.attachment.FileStoreSelector;
import io.github.chirino.memory.config.AttachmentConfig;
import io.github.chirino.memory.grpc.v1.AttachmentInfo;
import io.github.chirino.memory.grpc.v1.AttachmentsService;
import io.github.chirino.memory.grpc.v1.DownloadAttachmentRequest;
import io.github.chirino.memory.grpc.v1.DownloadAttachmentResponse;
import io.github.chirino.memory.grpc.v1.GetAttachmentRequest;
import io.github.chirino.memory.grpc.v1.UploadAttachmentRequest;
import io.github.chirino.memory.grpc.v1.UploadAttachmentResponse;
import io.github.chirino.memory.store.AccessDeniedException;
import io.github.chirino.memory.store.ResourceNotFoundException;
import io.quarkus.grpc.GrpcService;
import io.smallrye.common.annotation.Blocking;
import io.smallrye.mutiny.Multi;
import io.smallrye.mutiny.Uni;
import jakarta.inject.Inject;
import java.io.ByteArrayInputStream;
import java.io.ByteArrayOutputStream;
import java.io.InputStream;
import java.security.DigestInputStream;
import java.security.MessageDigest;
import java.time.Duration;
import java.time.Instant;
import java.util.HexFormat;
import java.util.List;
import org.jboss.logging.Logger;

@GrpcService
@Blocking
public class AttachmentsGrpcService extends AbstractGrpcService implements AttachmentsService {

    private static final Logger LOG = Logger.getLogger(AttachmentsGrpcService.class);
    private static final int DOWNLOAD_CHUNK_SIZE = 65536;

    @Inject AttachmentStoreSelector attachmentStoreSelector;

    @Inject FileStoreSelector fileStoreSelector;

    @Inject AttachmentConfig config;

    private AttachmentStore attachmentStore() {
        return attachmentStoreSelector.getStore();
    }

    private FileStore fileStore() {
        return fileStoreSelector.getFileStore();
    }

    @Override
    public Uni<UploadAttachmentResponse> uploadAttachment(
            Multi<UploadAttachmentRequest> requestStream) {
        // Collect all messages reactively, then process on a blocking thread
        return requestStream
                .collect()
                .asList()
                .onItem()
                .transform(this::processUpload)
                .onFailure()
                .transform(GrpcStatusMapper::map);
    }

    private UploadAttachmentResponse processUpload(List<UploadAttachmentRequest> messages) {
        if (messages.isEmpty()) {
            throw new IllegalArgumentException(
                    "Empty upload stream: at least one message with metadata is required");
        }

        // First message must contain metadata
        UploadAttachmentRequest first = messages.get(0);
        if (!first.hasMetadata()) {
            throw new IllegalArgumentException("First message must contain upload metadata");
        }

        var meta = first.getMetadata();
        String filename = meta.getFilename();
        String contentType =
                meta.getContentType().isEmpty()
                        ? "application/octet-stream"
                        : meta.getContentType();

        // Parse and validate expiresIn
        Duration expiresDuration = config.getDefaultExpiresIn();
        if (!meta.getExpiresIn().isEmpty()) {
            try {
                expiresDuration = Duration.parse(meta.getExpiresIn());
            } catch (Exception e) {
                throw new IllegalArgumentException(
                        "Invalid expiresIn format. Use ISO-8601 duration (e.g., PT1H)");
            }
            if (expiresDuration.compareTo(config.getMaxExpiresIn()) > 0) {
                throw new IllegalArgumentException(
                        "expiresIn exceeds maximum allowed duration of "
                                + config.getMaxExpiresIn());
            }
        }

        Instant finalExpiresAt = Instant.now().plus(expiresDuration);

        // Create attachment record with short upload expiry
        Instant uploadExpiresAt = Instant.now().plus(config.getUploadExpiresIn());
        AttachmentDto record =
                attachmentStore().create(currentUserId(), contentType, filename, uploadExpiresAt);

        try {
            // Collect all chunk bytes into a ByteArrayOutputStream
            ByteArrayOutputStream baos = new ByteArrayOutputStream();
            for (UploadAttachmentRequest msg : messages) {
                if (msg.hasChunk()) {
                    msg.getChunk().writeTo(baos);
                }
            }

            // Wrap in DigestInputStream to compute SHA-256 on the fly
            ByteArrayInputStream byteStream = new ByteArrayInputStream(baos.toByteArray());
            MessageDigest sha256Digest = MessageDigest.getInstance("SHA-256");
            DigestInputStream digestStream = new DigestInputStream(byteStream, sha256Digest);

            // Store in FileStore (handles size enforcement and chunked I/O)
            FileStoreResult storeResult =
                    fileStore().store(digestStream, config.getMaxSize(), contentType);

            String sha256Hex = HexFormat.of().formatHex(sha256Digest.digest());

            // Update attachment record
            attachmentStore()
                    .updateAfterUpload(
                            record.id(),
                            storeResult.storageKey(),
                            storeResult.size(),
                            sha256Hex,
                            finalExpiresAt);

            // Build response
            return UploadAttachmentResponse.newBuilder()
                    .setId(record.id())
                    .setHref("/v1/attachments/" + record.id())
                    .setContentType(contentType)
                    .setFilename(filename != null ? filename : "")
                    .setSize(storeResult.size())
                    .setSha256(sha256Hex)
                    .setExpiresAt(finalExpiresAt.toString())
                    .build();
        } catch (Exception e) {
            // Cleanup on failure
            try {
                attachmentStore().delete(record.id());
            } catch (Exception cleanupEx) {
                LOG.warnf(cleanupEx, "Failed to cleanup attachment record %s", record.id());
            }
            if (e instanceof RuntimeException re) {
                throw re;
            }
            throw new RuntimeException("Upload failed", e);
        }
    }

    @Override
    public Uni<AttachmentInfo> getAttachment(GetAttachmentRequest request) {
        return Uni.createFrom()
                .item(
                        () -> {
                            String id = request.getId();
                            if (id == null || id.isEmpty()) {
                                throw new IllegalArgumentException("Attachment ID is required");
                            }

                            AttachmentDto att =
                                    attachmentStore()
                                            .findById(id)
                                            .orElseThrow(
                                                    () ->
                                                            new ResourceNotFoundException(
                                                                    "attachment", id));

                            // Access control: if unlinked, only uploader can access
                            if (att.entryId() == null && !att.userId().equals(currentUserId())) {
                                throw new AccessDeniedException("Access denied to this attachment");
                            }

                            return toAttachmentInfo(att);
                        })
                .onFailure()
                .transform(GrpcStatusMapper::map);
    }

    @Override
    public Multi<DownloadAttachmentResponse> downloadAttachment(DownloadAttachmentRequest request) {
        return Multi.createFrom()
                .emitter(
                        emitter -> {
                            try {
                                String id = request.getId();
                                if (id == null || id.isEmpty()) {
                                    throw new IllegalArgumentException("Attachment ID is required");
                                }

                                AttachmentDto att =
                                        attachmentStore()
                                                .findById(id)
                                                .orElseThrow(
                                                        () ->
                                                                new ResourceNotFoundException(
                                                                        "attachment", id));

                                // Access control
                                if (att.entryId() == null
                                        && !att.userId().equals(currentUserId())) {
                                    throw new AccessDeniedException(
                                            "Access denied to this attachment");
                                }

                                if (att.storageKey() == null) {
                                    throw new ResourceNotFoundException("attachment content", id);
                                }

                                // First emission: metadata
                                emitter.emit(
                                        DownloadAttachmentResponse.newBuilder()
                                                .setMetadata(toAttachmentInfo(att))
                                                .build());

                                // Stream file chunks
                                try (InputStream stream = fileStore().retrieve(att.storageKey())) {
                                    byte[] buffer = new byte[DOWNLOAD_CHUNK_SIZE];
                                    int bytesRead;
                                    while ((bytesRead = stream.read(buffer)) != -1) {
                                        emitter.emit(
                                                DownloadAttachmentResponse.newBuilder()
                                                        .setChunk(
                                                                ByteString.copyFrom(
                                                                        buffer, 0, bytesRead))
                                                        .build());
                                    }
                                }

                                emitter.complete();
                            } catch (Exception e) {
                                emitter.fail(GrpcStatusMapper.map(e));
                            }
                        });
    }

    private static AttachmentInfo toAttachmentInfo(AttachmentDto att) {
        var builder =
                AttachmentInfo.newBuilder()
                        .setId(att.id())
                        .setHref("/v1/attachments/" + att.id())
                        .setContentType(att.contentType() != null ? att.contentType() : "");

        if (att.filename() != null) {
            builder.setFilename(att.filename());
        }
        if (att.size() != null) {
            builder.setSize(att.size());
        }
        if (att.sha256() != null) {
            builder.setSha256(att.sha256());
        }
        if (att.expiresAt() != null) {
            builder.setExpiresAt(att.expiresAt().toString());
        }
        if (att.createdAt() != null) {
            builder.setCreatedAt(att.createdAt().toString());
        }
        return builder.build();
    }
}
