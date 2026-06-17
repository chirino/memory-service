package io.github.chirino.memoryservice.history;

import com.fasterxml.jackson.annotation.JsonIgnore;
import java.nio.file.Path;
import java.util.LinkedHashMap;
import java.util.Map;
import org.springframework.ai.content.Media;
import org.springframework.core.io.FileSystemResource;
import org.springframework.lang.Nullable;

/**
 * Describes an attachment for history recording and optional lazy Spring AI media delivery.
 */
public record AttachmentDescriptor(
        @Nullable String attachmentId,
        @Nullable String contentType,
        @Nullable String name,
        @Nullable String href,
        @JsonIgnore @Nullable Path filePath,
        @JsonIgnore @Nullable String contentUrl) {

    public AttachmentDescriptor(
            @Nullable String attachmentId,
            @Nullable String contentType,
            @Nullable String name,
            @Nullable String href) {
        this(attachmentId, contentType, name, href, null, null);
    }

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
     * Resolves this descriptor into Spring AI media for LLM delivery.
     */
    @Nullable
    public Media media() {
        if (contentUrl != null) {
            return AttachmentResolver.toMediaFromUrl(contentType, contentUrl);
        }
        if (filePath != null) {
            return AttachmentResolver.toMediaFromResource(
                    contentType, new FileSystemResource(filePath));
        }
        return null;
    }

    Map<String, Object> toHistoryMap() {
        Map<String, Object> map = new LinkedHashMap<>();
        if (attachmentId != null && !attachmentId.isBlank()) {
            map.put("attachmentId", attachmentId);
        }
        if (href != null && !href.isBlank()) {
            map.put("href", href);
        }
        if (contentType != null && !contentType.isBlank()) {
            map.put("contentType", contentType);
        }
        if (name != null && !name.isBlank()) {
            map.put("name", name);
        }
        return map;
    }
}
