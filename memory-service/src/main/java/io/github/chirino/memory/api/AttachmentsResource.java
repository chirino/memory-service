package io.github.chirino.memory.api;

import io.github.chirino.memory.attachment.AttachmentDto;
import io.github.chirino.memory.attachment.AttachmentStore;
import io.github.chirino.memory.attachment.AttachmentStoreSelector;
import io.github.chirino.memory.attachment.DownloadUrlSigner;
import io.github.chirino.memory.attachment.FileStore;
import io.github.chirino.memory.attachment.FileStoreException;
import io.github.chirino.memory.attachment.FileStoreResult;
import io.github.chirino.memory.attachment.FileStoreSelector;
import io.github.chirino.memory.client.model.ErrorResponse;
import io.github.chirino.memory.config.AttachmentConfig;
import io.quarkus.security.Authenticated;
import io.quarkus.security.identity.SecurityIdentity;
import jakarta.inject.Inject;
import jakarta.ws.rs.Consumes;
import jakarta.ws.rs.DELETE;
import jakarta.ws.rs.GET;
import jakarta.ws.rs.POST;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.PathParam;
import jakarta.ws.rs.Produces;
import jakarta.ws.rs.QueryParam;
import jakarta.ws.rs.core.MediaType;
import jakarta.ws.rs.core.Response;
import java.io.InputStream;
import java.net.URI;
import java.net.URLEncoder;
import java.nio.charset.StandardCharsets;
import java.security.DigestInputStream;
import java.security.MessageDigest;
import java.time.Duration;
import java.time.Instant;
import java.util.HashMap;
import java.util.HexFormat;
import java.util.Map;
import java.util.Optional;
import org.jboss.logging.Logger;
import org.jboss.resteasy.reactive.server.multipart.FormValue;
import org.jboss.resteasy.reactive.server.multipart.MultipartFormDataInput;

@Path("/v1/attachments")
@Authenticated
public class AttachmentsResource {

    private static final Logger LOG = Logger.getLogger(AttachmentsResource.class);

    @Inject SecurityIdentity identity;

    @Inject AttachmentStoreSelector attachmentStoreSelector;

    @Inject FileStoreSelector fileStoreSelector;

    @Inject AttachmentConfig config;

    @Inject DownloadUrlSigner downloadUrlSigner;

    private AttachmentStore attachmentStore() {
        return attachmentStoreSelector.getStore();
    }

    private FileStore fileStore() {
        return fileStoreSelector.getFileStore();
    }

    private String currentUserId() {
        return identity.getPrincipal().getName();
    }

    @POST
    @Consumes(MediaType.MULTIPART_FORM_DATA)
    @Produces(MediaType.APPLICATION_JSON)
    public Response upload(
            MultipartFormDataInput input, @QueryParam("expiresIn") String expiresIn) {
        // Parse and validate expiresIn
        Duration expiresDuration = config.getDefaultExpiresIn();
        if (expiresIn != null && !expiresIn.isBlank()) {
            try {
                expiresDuration = Duration.parse(expiresIn);
            } catch (Exception e) {
                return badRequest("Invalid expiresIn format. Use ISO-8601 duration (e.g., PT1H)");
            }
            if (expiresDuration.compareTo(config.getMaxExpiresIn()) > 0) {
                return badRequest(
                        "expiresIn exceeds maximum allowed duration of "
                                + config.getMaxExpiresIn());
            }
        }

        // Extract file from multipart form
        var fileEntry = input.getValues().get("file");
        if (fileEntry == null || fileEntry.isEmpty()) {
            return badRequest("No file provided. Use 'file' as the form field name.");
        }

        FormValue formValue = fileEntry.iterator().next();
        if (!formValue.isFileItem()) {
            return badRequest("The 'file' field must be a file upload.");
        }

        String contentType =
                formValue.getHeaders().getFirst("Content-Type") != null
                        ? formValue.getHeaders().getFirst("Content-Type")
                        : "application/octet-stream";
        String filename = formValue.getFileName();

        Instant finalExpiresAt = Instant.now().plus(expiresDuration);

        // Create attachment record with short upload expiry
        Instant uploadExpiresAt = Instant.now().plus(config.getUploadExpiresIn());
        AttachmentDto record =
                attachmentStore().create(currentUserId(), contentType, filename, uploadExpiresAt);

        try {
            // Wrap the upload stream in a DigestInputStream to compute SHA-256 on the fly.
            // The FileStore reads through this stream, so the digest is computed as a
            // side effect of storage - no separate buffering needed in this layer.
            InputStream fileStream = formValue.getFileItem().getInputStream();
            MessageDigest sha256Digest = MessageDigest.getInstance("SHA-256");
            DigestInputStream digestStream = new DigestInputStream(fileStream, sha256Digest);

            // Store in FileStore (handles size enforcement and chunked I/O)
            FileStoreResult storeResult =
                    fileStoreSelector
                            .getFileStore()
                            .store(digestStream, config.getMaxSize(), contentType);

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
            Map<String, Object> response = new HashMap<>();
            response.put("id", record.id());
            response.put("href", "/v1/attachments/" + record.id());
            response.put("contentType", contentType);
            response.put("filename", filename);
            response.put("size", storeResult.size());
            response.put("sha256", sha256Hex);
            response.put("expiresAt", finalExpiresAt.toString());

            return Response.status(Response.Status.CREATED).entity(response).build();
        } catch (FileStoreException e) {
            attachmentStore().delete(record.id());
            return fileStoreErrorResponse(e);
        } catch (Exception e) {
            LOG.errorf(e, "Failed to upload attachment");
            attachmentStore().delete(record.id());
            return Response.status(Response.Status.INTERNAL_SERVER_ERROR)
                    .entity(errorResponse("Upload failed", "internal_error", Map.of()))
                    .build();
        }
    }

    @GET
    @Path("/{id}")
    public Response retrieve(@PathParam("id") String id) {
        Optional<AttachmentDto> optAtt = attachmentStore().findById(id);
        if (optAtt.isEmpty()) {
            return Response.status(Response.Status.NOT_FOUND)
                    .entity(
                            errorResponse(
                                    "Not found", "not_found", Map.of("resource", "attachment")))
                    .build();
        }

        AttachmentDto att = optAtt.get();

        // Access control: if unlinked, only uploader can access
        if (att.entryId() == null) {
            if (!att.userId().equals(currentUserId())) {
                return Response.status(Response.Status.FORBIDDEN)
                        .entity(
                                errorResponse(
                                        "Forbidden",
                                        "forbidden",
                                        Map.of("message", "Access denied to this attachment")))
                        .build();
            }
        }
        // If linked to an entry, anyone who can see the conversation can see it.
        // For simplicity, we allow access if it has an entryId (the entry listing already
        // enforces conversation-level access control).

        if (att.storageKey() == null) {
            return Response.status(Response.Status.NOT_FOUND)
                    .entity(
                            errorResponse(
                                    "Not found",
                                    "not_found",
                                    Map.of("message", "Attachment content not available")))
                    .build();
        }

        // Check for signed URL redirect
        Optional<URI> signedUrl = fileStore().getSignedUrl(att.storageKey(), Duration.ofHours(1));
        if (signedUrl.isPresent()) {
            return Response.temporaryRedirect(signedUrl.get()).build();
        }

        // Stream bytes directly
        try {
            InputStream stream = fileStore().retrieve(att.storageKey());
            Response.ResponseBuilder builder =
                    Response.ok(stream).header("Content-Type", att.contentType());
            if (att.size() != null) {
                builder.header("Content-Length", att.size());
            }
            if (att.filename() != null) {
                builder.header(
                        "Content-Disposition", "inline; filename=\"" + att.filename() + "\"");
            }
            return builder.build();
        } catch (FileStoreException e) {
            return fileStoreErrorResponse(e);
        }
    }

    @GET
    @Path("/{id}/download-url")
    @Produces(MediaType.APPLICATION_JSON)
    public Response getDownloadUrl(@PathParam("id") String id) {
        Optional<AttachmentDto> optAtt = attachmentStore().findById(id);
        if (optAtt.isEmpty()) {
            return Response.status(Response.Status.NOT_FOUND)
                    .entity(
                            errorResponse(
                                    "Not found", "not_found", Map.of("resource", "attachment")))
                    .build();
        }

        AttachmentDto att = optAtt.get();

        // Access control: same as retrieve()
        if (att.entryId() == null) {
            if (!att.userId().equals(currentUserId())) {
                return Response.status(Response.Status.FORBIDDEN)
                        .entity(
                                errorResponse(
                                        "Forbidden",
                                        "forbidden",
                                        Map.of("message", "Access denied to this attachment")))
                        .build();
            }
        }

        if (att.storageKey() == null) {
            return Response.status(Response.Status.NOT_FOUND)
                    .entity(
                            errorResponse(
                                    "Not found",
                                    "not_found",
                                    Map.of("message", "Attachment content not available")))
                    .build();
        }

        Duration expiry = config.getDownloadUrlExpiresIn();

        // For S3: return the pre-signed URL directly
        Optional<URI> signedUrl = fileStore().getSignedUrl(att.storageKey(), expiry);
        if (signedUrl.isPresent()) {
            Map<String, Object> response = new HashMap<>();
            response.put("url", signedUrl.get().toString());
            response.put("expiresIn", (int) expiry.toSeconds());
            return Response.ok(response).build();
        }

        // For DB: generate an HMAC-signed token
        String token = downloadUrlSigner.createToken(id, expiry);
        String filename =
                att.filename() != null
                        ? URLEncoder.encode(att.filename(), StandardCharsets.UTF_8)
                        : "attachment";
        String url = "/v1/attachments/download/" + token + "/" + filename;

        Map<String, Object> response = new HashMap<>();
        response.put("url", url);
        response.put("expiresIn", (int) expiry.toSeconds());
        return Response.ok(response).build();
    }

    @DELETE
    @Path("/{id}")
    public Response delete(@PathParam("id") String id) {
        Optional<AttachmentDto> optAtt = attachmentStore().findById(id);
        if (optAtt.isEmpty()) {
            return Response.status(Response.Status.NOT_FOUND)
                    .entity(
                            errorResponse(
                                    "Not found", "not_found", Map.of("resource", "attachment")))
                    .build();
        }

        AttachmentDto att = optAtt.get();

        // Only the uploader can delete
        if (!att.userId().equals(currentUserId())) {
            return Response.status(Response.Status.FORBIDDEN)
                    .type(MediaType.APPLICATION_JSON)
                    .entity(
                            errorResponse(
                                    "Forbidden",
                                    "forbidden",
                                    Map.of(
                                            "message",
                                            "Only the uploader can delete this attachment")))
                    .build();
        }

        // Cannot delete linked attachments
        if (att.entryId() != null) {
            return Response.status(Response.Status.CONFLICT)
                    .type(MediaType.APPLICATION_JSON)
                    .entity(
                            errorResponse(
                                    "Conflict",
                                    "attachment_linked",
                                    Map.of(
                                            "message",
                                            "Attachment is linked to an entry and cannot be"
                                                    + " deleted")))
                    .build();
        }

        // Delete from FileStore first, then AttachmentStore
        try {
            if (att.storageKey() != null) {
                fileStore().delete(att.storageKey());
            }
        } catch (Exception e) {
            LOG.warnf(e, "Failed to delete file from store for attachment %s", id);
            // Continue with metadata deletion even if file deletion fails
        }

        attachmentStore().delete(id);
        return Response.noContent().build();
    }

    private Response fileStoreErrorResponse(FileStoreException e) {
        Map<String, Object> details = new HashMap<>(e.getDetails());
        details.put("message", e.getMessage());
        return Response.status(e.getHttpStatus())
                .type(MediaType.APPLICATION_JSON)
                .entity(errorResponse(e.getMessage(), e.getCode(), details))
                .build();
    }

    private Response badRequest(String message) {
        return Response.status(Response.Status.BAD_REQUEST)
                .type(MediaType.APPLICATION_JSON)
                .entity(errorResponse("Invalid request", "bad_request", Map.of("message", message)))
                .build();
    }

    private ErrorResponse errorResponse(String error, String code, Map<String, Object> details) {
        ErrorResponse resp = new ErrorResponse();
        resp.setError(error);
        resp.setCode(code);
        resp.setDetails(details);
        return resp;
    }
}
