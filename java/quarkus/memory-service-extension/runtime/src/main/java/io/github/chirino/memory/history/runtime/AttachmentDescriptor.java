package io.github.chirino.memory.history.runtime;

import com.fasterxml.jackson.annotation.JsonIgnore;
import dev.langchain4j.data.message.Content;
import java.nio.file.Path;

/**
 * Describes an attachment for history recording and optional lazy LLM content delivery.
 *
 * @param attachmentId the attachment ID (UUID string)
 * @param contentType the MIME type (e.g. "image/png", "audio/mp3"), may be null
 * @param name the original filename, may be null
 * @param href the download URL or reference, may be null
 * @param filePath local temporary file path for lazy content conversion, not serialized
 * @param contentUrl signed remote URL for lazy content conversion, not serialized
 */
public record AttachmentDescriptor(
        String attachmentId,
        String contentType,
        String name,
        String href,
        @JsonIgnore Path filePath,
        @JsonIgnore String contentUrl) {

    public AttachmentDescriptor(String attachmentId, String contentType, String name, String href) {
        this(attachmentId, contentType, name, href, null, null);
    }

    /** Creates a descriptor from an {@link AttachmentRef}. */
    public static AttachmentDescriptor fromRef(AttachmentRef ref) {
        return new AttachmentDescriptor(ref.id(), ref.contentType(), ref.name(), ref.href());
    }

    AttachmentDescriptor withFilePath(String resolvedContentType, Path filePath) {
        return new AttachmentDescriptor(
                attachmentId, resolvedContentType, name, href, filePath, null);
    }

    AttachmentDescriptor withContentUrl(String contentUrl) {
        return new AttachmentDescriptor(attachmentId, contentType, name, href, null, contentUrl);
    }

    /**
     * Resolves this descriptor into LangChain4j content for LLM delivery.
     */
    public Content content() {
        if (contentUrl != null) {
            return AttachmentResolver.toContentFromUrl(contentType, contentUrl);
        }
        if (filePath != null) {
            return AttachmentResolver.toContentFromPath(contentType, filePath);
        }
        return null;
    }
}
