package io.github.chirino.memoryservice.history;

import java.util.List;
import java.util.Map;
import org.springframework.ai.content.Media;

/**
 * Holds both the attachment metadata (for history recording) and the resolved Spring AI {@link
 * Media} objects (for LLM delivery). Created by {@link AttachmentResolver}.
 *
 * <p>When passed via the advisor context using {@link
 * ConversationHistoryStreamAdvisor#ATTACHMENTS_KEY}, the advisor automatically extracts {@link
 * #metadata()} for conversation history storage.
 */
public class Attachments {

    private static final Attachments EMPTY = new Attachments(List.of(), List.of());

    private final List<Map<String, Object>> metadata;
    private final List<Media> media;

    public Attachments(List<Map<String, Object>> metadata, List<Media> media) {
        this.metadata = metadata;
        this.media = media;
    }

    /** Attachment metadata for history recording (attachmentId, contentType, name). */
    public List<Map<String, Object>> metadata() {
        return metadata;
    }

    /** Resolved Spring AI Media objects for LLM delivery. */
    public List<Media> media() {
        return media;
    }

    public boolean isEmpty() {
        return media.isEmpty() && metadata.isEmpty();
    }

    public static Attachments empty() {
        return EMPTY;
    }
}
