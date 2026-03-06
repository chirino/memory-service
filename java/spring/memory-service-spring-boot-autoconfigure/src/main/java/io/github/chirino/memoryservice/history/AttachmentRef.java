package io.github.chirino.memoryservice.history;

/**
 * Simple reference to an uploaded attachment. Used as input to {@link AttachmentResolver}.
 *
 * @param id the attachment ID (UUID string), may be null if href is provided
 * @param contentType the MIME type hint (e.g. "image/png", "audio/mp3"), may be null
 * @param name the original filename, may be null
 * @param href explicit URL for the attachment, may be null (falls back to /v1/attachments/{id})
 */
public record AttachmentRef(String id, String contentType, String name, String href) {

    public AttachmentRef(String id, String contentType, String name) {
        this(id, contentType, name, null);
    }
}
