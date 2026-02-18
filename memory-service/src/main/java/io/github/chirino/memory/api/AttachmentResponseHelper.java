package io.github.chirino.memory.api;

import io.github.chirino.memory.attachment.AttachmentDto;
import jakarta.ws.rs.core.Response;
import java.util.Optional;

/** Shared cache-header logic for attachment download endpoints. */
public final class AttachmentResponseHelper {

    private AttachmentResponseHelper() {}

    /**
     * Check If-None-Match against the attachment's sha256. Returns an Optional 304 response if the
     * ETag matches.
     */
    public static Optional<Response> checkNotModified(
            String ifNoneMatch, AttachmentDto att, String cacheControl) {
        if (att.sha256() != null && ifNoneMatch != null) {
            String etag = "\"" + att.sha256() + "\"";
            // Handle both quoted and unquoted, plus wildcard and multi-value
            if (ifNoneMatch.equals(etag)
                    || ifNoneMatch.equals(att.sha256())
                    || ifNoneMatch.contains(etag)) {
                return Optional.of(
                        Response.notModified()
                                .header("ETag", etag)
                                .header("Cache-Control", cacheControl)
                                .build());
            }
        }
        return Optional.empty();
    }

    /** Add ETag and Cache-Control to a response builder. */
    public static void addCacheHeaders(
            Response.ResponseBuilder builder, AttachmentDto att, String cacheControl) {
        builder.header("Cache-Control", cacheControl);
        if (att.sha256() != null) {
            builder.header("ETag", "\"" + att.sha256() + "\"");
        }
    }
}
