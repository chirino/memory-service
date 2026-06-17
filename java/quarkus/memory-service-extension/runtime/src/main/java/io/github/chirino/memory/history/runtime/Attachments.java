package io.github.chirino.memory.history.runtime;

import dev.langchain4j.data.message.Content;
import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.List;
import org.jboss.logging.Logger;

/**
 * Holds both attachment descriptors (for history recording) and resolved LangChain4j Content
 * objects (for LLM delivery). Created by {@link AttachmentResolver}.
 *
 * <p>When passed as a parameter to a {@link
 * io.github.chirino.memory.history.annotations.RecordConversation @RecordConversation} method, the
 * {@link ConversationInterceptor} automatically extracts {@link #descriptors()} for conversation
 * history storage.
 *
 * <p>This class owns temporary files created during attachment resolution. Call {@link #close()}
 * after the model request completes.
 */
public class Attachments implements AutoCloseable {

    private static final Logger LOG = Logger.getLogger(Attachments.class);
    private static final Attachments EMPTY = new Attachments(List.of(), List.of());

    private final List<AttachmentDescriptor> descriptors;
    private volatile List<Content> contents;

    public Attachments(List<AttachmentDescriptor> descriptors, List<Content> contents) {
        this.descriptors = descriptors;
        this.contents = contents;
    }

    static Attachments fromDescriptors(List<AttachmentDescriptor> descriptors) {
        return new Attachments(descriptors, null);
    }

    /** Attachment descriptors for history recording (attachmentId, contentType, name, href). */
    public List<AttachmentDescriptor> descriptors() {
        return descriptors;
    }

    /** Resolved LangChain4j Content objects for LLM delivery. Built lazily on first access. */
    public List<Content> contents() {
        List<Content> resolved = contents;
        if (resolved == null) {
            synchronized (this) {
                resolved = contents;
                if (resolved == null) {
                    resolved =
                            descriptors.stream()
                                    .map(AttachmentDescriptor::content)
                                    .filter(content -> content != null)
                                    .toList();
                    contents = resolved;
                }
            }
        }
        return resolved;
    }

    public boolean isEmpty() {
        List<Content> resolved = contents;
        return descriptors.isEmpty() && (resolved == null || resolved.isEmpty());
    }

    public static Attachments empty() {
        return EMPTY;
    }

    @Override
    public void close() {
        for (AttachmentDescriptor item : descriptors) {
            close(item);
        }
    }

    private static void close(AttachmentDescriptor item) {
        Path filePath = item.filePath();
        if (filePath == null) {
            return;
        }
        try {
            Files.deleteIfExists(filePath);
            LOG.debugf("Deleted temporary attachment file: %s", filePath);
        } catch (IOException e) {
            LOG.warnf(e, "Failed to delete temporary attachment file: %s", filePath);
        }
    }
}
