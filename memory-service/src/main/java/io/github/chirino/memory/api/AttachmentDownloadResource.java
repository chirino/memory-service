package io.github.chirino.memory.api;

import io.github.chirino.memory.attachment.AttachmentDto;
import io.github.chirino.memory.attachment.AttachmentStoreSelector;
import io.github.chirino.memory.attachment.DownloadUrlSigner;
import io.github.chirino.memory.attachment.FileStoreException;
import io.github.chirino.memory.attachment.FileStoreSelector;
import jakarta.inject.Inject;
import jakarta.ws.rs.GET;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.PathParam;
import jakarta.ws.rs.core.Response;
import java.io.InputStream;
import java.time.Instant;
import java.util.Optional;
import org.jboss.logging.Logger;

@Path("/v1/attachments/download")
public class AttachmentDownloadResource {

    private static final Logger LOG = Logger.getLogger(AttachmentDownloadResource.class);

    @Inject DownloadUrlSigner downloadUrlSigner;

    @Inject AttachmentStoreSelector attachmentStoreSelector;

    @Inject FileStoreSelector fileStoreSelector;

    @GET
    @Path("/{token}/{filename}")
    public Response download(
            @PathParam("token") String token, @PathParam("filename") String filename) {
        // Verify token
        Optional<DownloadUrlSigner.SignedDownloadClaim> claim =
                downloadUrlSigner.verifyToken(token);
        if (claim.isEmpty()) {
            return Response.status(Response.Status.FORBIDDEN)
                    .entity("Invalid or expired token")
                    .build();
        }

        String attachmentId = claim.get().attachmentId();
        Instant expiresAt = claim.get().expiresAt();

        // Lookup attachment
        Optional<AttachmentDto> optAtt = attachmentStoreSelector.getStore().findById(attachmentId);
        if (optAtt.isEmpty()) {
            return Response.status(Response.Status.NOT_FOUND)
                    .entity("Attachment not found")
                    .build();
        }

        AttachmentDto att = optAtt.get();
        if (att.storageKey() == null) {
            return Response.status(Response.Status.NOT_FOUND)
                    .entity("Attachment content not available")
                    .build();
        }

        // Stream the file
        try {
            InputStream stream = fileStoreSelector.getFileStore().retrieve(att.storageKey());

            long cacheMaxAge =
                    Math.max(0, expiresAt.getEpochSecond() - Instant.now().getEpochSecond());

            Response.ResponseBuilder builder =
                    Response.ok(stream).header("Content-Type", att.contentType());
            if (att.size() != null) {
                builder.header("Content-Length", att.size());
            }
            if (att.filename() != null) {
                builder.header(
                        "Content-Disposition", "inline; filename=\"" + att.filename() + "\"");
            }
            builder.header("Cache-Control", "private, max-age=" + cacheMaxAge);
            return builder.build();
        } catch (FileStoreException e) {
            LOG.errorf(e, "Failed to retrieve attachment %s", attachmentId);
            return Response.status(e.getHttpStatus()).entity(e.getMessage()).build();
        }
    }
}
