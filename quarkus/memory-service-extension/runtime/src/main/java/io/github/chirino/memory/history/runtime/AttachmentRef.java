package io.github.chirino.memory.history.runtime;

/**
 * Simple reference to an uploaded attachment. Used as input to {@link AttachmentResolver}.
 *
 * @param id the attachment ID (UUID string)
 * @param contentType the MIME type hint (e.g. "image/png", "audio/mp3"), may be null
 * @param name the original filename, may be null
 */
public record AttachmentRef(String id, String contentType, String name) {}
