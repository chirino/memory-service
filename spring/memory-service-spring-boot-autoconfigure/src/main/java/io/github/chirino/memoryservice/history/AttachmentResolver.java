package io.github.chirino.memoryservice.history;

import java.net.URI;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import org.springframework.ai.content.Media;
import org.springframework.util.MimeTypeUtils;
import org.springframework.util.StringUtils;

/**
 * Resolves {@link AttachmentRef} references into {@link Attachments} by building both attachment
 * metadata (for history recording) and Spring AI {@link Media} objects (for LLM delivery).
 */
public class AttachmentResolver {

    /**
     * Resolves a list of attachment references into an {@link Attachments} object containing both
     * metadata and Media objects.
     */
    public Attachments resolve(List<AttachmentRef> refs) {
        if (refs == null || refs.isEmpty()) {
            return Attachments.empty();
        }

        List<Map<String, Object>> metadata = new ArrayList<>();
        List<Media> media = new ArrayList<>();

        for (AttachmentRef ref : refs) {
            if (!StringUtils.hasText(ref.id()) && !StringUtils.hasText(ref.href())) {
                continue;
            }

            // Build metadata for history recording
            if (StringUtils.hasText(ref.id())) {
                Map<String, Object> meta = new LinkedHashMap<>();
                meta.put("attachmentId", ref.id());
                if (StringUtils.hasText(ref.contentType())) {
                    meta.put("contentType", ref.contentType());
                }
                if (StringUtils.hasText(ref.name())) {
                    meta.put("name", ref.name());
                }
                metadata.add(meta);
            }

            // Build Media for LLM delivery
            String resolvedHref = ref.href();
            if (!StringUtils.hasText(resolvedHref) && StringUtils.hasText(ref.id())) {
                resolvedHref = "/v1/attachments/" + ref.id();
            }
            if (StringUtils.hasText(resolvedHref)) {
                var mimeType =
                        StringUtils.hasText(ref.contentType())
                                ? MimeTypeUtils.parseMimeType(ref.contentType())
                                : MimeTypeUtils.APPLICATION_OCTET_STREAM;
                media.add(new Media(mimeType, URI.create(resolvedHref)));
            }
        }

        return new Attachments(metadata, media);
    }
}
