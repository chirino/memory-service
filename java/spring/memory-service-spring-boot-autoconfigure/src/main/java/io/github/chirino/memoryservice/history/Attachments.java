package io.github.chirino.memoryservice.history;

import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.List;
import java.util.Map;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.ai.content.Media;

/**
 * Holds both attachment descriptors (for history recording) and resolved Spring AI {@link Media}
 * objects (for LLM delivery). Created by {@link AttachmentResolver}.
 *
 * <p>When passed via the advisor context using {@link
 * ConversationHistoryStreamAdvisor#ATTACHMENTS_KEY}, the advisor automatically extracts {@link
 * #descriptors()} for conversation history storage.
 */
public class Attachments implements AutoCloseable {

    private static final Logger LOG = LoggerFactory.getLogger(Attachments.class);
    private static final Attachments EMPTY = new Attachments(List.of(), List.of());

    private final List<AttachmentDescriptor> descriptors;
    private volatile List<Media> media;

    public Attachments(List<AttachmentDescriptor> descriptors, List<Media> media) {
        this.descriptors = descriptors;
        this.media = media;
    }

    static Attachments fromDescriptors(List<AttachmentDescriptor> descriptors) {
        return new Attachments(descriptors, null);
    }

    /** Attachment descriptors for history recording (attachmentId, contentType, name, href). */
    public List<AttachmentDescriptor> descriptors() {
        return descriptors;
    }

    /** Attachment metadata maps for existing history storage APIs. */
    public List<Map<String, Object>> toHistoryMaps() {
        return descriptors.stream().map(AttachmentDescriptor::toHistoryMap).toList();
    }

    /** Resolved Spring AI Media objects for LLM delivery. Built lazily on first access. */
    public List<Media> media() {
        List<Media> resolved = media;
        if (resolved == null) {
            synchronized (this) {
                resolved = media;
                if (resolved == null) {
                    resolved =
                            descriptors.stream()
                                    .map(AttachmentDescriptor::media)
                                    .filter(item -> item != null)
                                    .toList();
                    media = resolved;
                }
            }
        }
        return resolved;
    }

    public boolean isEmpty() {
        List<Media> resolved = media;
        return descriptors.isEmpty() && (resolved == null || resolved.isEmpty());
    }

    public static Attachments empty() {
        return EMPTY;
    }

    @Override
    public void close() {
        for (AttachmentDescriptor descriptor : descriptors) {
            Path filePath = descriptor.filePath();
            if (filePath == null) {
                continue;
            }
            try {
                Files.deleteIfExists(filePath);
                LOG.debug("Deleted temporary attachment file: {}", filePath);
            } catch (IOException e) {
                LOG.warn("Failed to delete temporary attachment file: {}", filePath, e);
            }
        }
    }
}
