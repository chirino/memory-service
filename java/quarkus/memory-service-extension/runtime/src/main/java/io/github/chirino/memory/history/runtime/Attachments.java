package io.github.chirino.memory.history.runtime;

import dev.langchain4j.data.message.Content;
import java.util.List;
import java.util.Map;

/**
 * Holds both the attachment metadata (for history recording) and the resolved LangChain4j Content
 * objects (for LLM delivery). Created by {@link AttachmentResolver}.
 *
 * <p>When passed as a parameter to a {@link
 * io.github.chirino.memory.history.annotations.RecordConversation @RecordConversation} method, the
 * {@link ConversationInterceptor} automatically extracts {@link #metadata()} for conversation
 * history storage.
 */
public class Attachments {

    private static final Attachments EMPTY = new Attachments(List.of(), List.of());

    private final List<Map<String, Object>> metadata;
    private final List<Content> contents;

    public Attachments(List<Map<String, Object>> metadata, List<Content> contents) {
        this.metadata = metadata;
        this.contents = contents;
    }

    /** Attachment metadata for history recording (attachmentId, contentType, name). */
    public List<Map<String, Object>> metadata() {
        return metadata;
    }

    /** Resolved LangChain4j Content objects for LLM delivery. */
    public List<Content> contents() {
        return contents;
    }

    public boolean isEmpty() {
        return contents.isEmpty() && metadata.isEmpty();
    }

    public static Attachments empty() {
        return EMPTY;
    }
}
