package io.github.chirino.memory.store.impl;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import io.github.chirino.memory.api.dto.EntryDto;
import io.github.chirino.memory.client.model.CreateEntryRequest;
import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import java.util.Objects;

public final class MemorySyncHelper {

    private static final ObjectMapper OBJECT_MAPPER = new ObjectMapper();

    private MemorySyncHelper() {}

    /**
     * Flattens the content arrays from multiple entries into a single list. This is used to compare
     * the incoming sync request (single entry with all messages) against existing entries (which
     * may have content spread across multiple entries).
     */
    public static List<Object> flattenContent(List<EntryDto> entries) {
        if (entries == null || entries.isEmpty()) {
            return Collections.emptyList();
        }
        List<Object> result = new ArrayList<>();
        for (EntryDto entry : entries) {
            if (entry != null && entry.getContent() != null) {
                result.addAll(entry.getContent());
            }
        }
        return result;
    }

    /**
     * Checks if all entries have the same contentType as the incoming request. Returns true if
     * there's a contentType mismatch (diverged).
     */
    public static boolean hasContentTypeMismatch(
            List<EntryDto> entries, String incomingContentType) {
        if (entries == null || entries.isEmpty()) {
            return false;
        }
        for (EntryDto entry : entries) {
            if (entry != null && !Objects.equals(entry.getContentType(), incomingContentType)) {
                return true;
            }
        }
        return false;
    }

    /**
     * Compares two content lists for equality using JSON comparison to handle Map type differences.
     */
    public static boolean contentEquals(List<Object> existing, List<Object> incoming) {
        if (existing == null && incoming == null) {
            return true;
        }
        if (existing == null || incoming == null) {
            return false;
        }
        if (existing.size() != incoming.size()) {
            return false;
        }
        try {
            JsonNode existingNode = OBJECT_MAPPER.valueToTree(existing);
            JsonNode incomingNode = OBJECT_MAPPER.valueToTree(incoming);
            return existingNode.equals(incomingNode);
        } catch (Exception e) {
            return Objects.equals(existing, incoming);
        }
    }

    /**
     * Checks if the existing content is a prefix of the incoming content. Returns true if incoming
     * starts with all items from existing.
     */
    public static boolean isContentPrefix(List<Object> existing, List<Object> incoming) {
        if (existing == null || existing.isEmpty()) {
            return true;
        }
        if (incoming == null || incoming.size() < existing.size()) {
            return false;
        }
        try {
            for (int i = 0; i < existing.size(); i++) {
                JsonNode existingNode = OBJECT_MAPPER.valueToTree(existing.get(i));
                JsonNode incomingNode = OBJECT_MAPPER.valueToTree(incoming.get(i));
                if (!existingNode.equals(incomingNode)) {
                    return false;
                }
            }
            return true;
        } catch (Exception e) {
            // Fallback to direct comparison
            for (int i = 0; i < existing.size(); i++) {
                if (!Objects.equals(existing.get(i), incoming.get(i))) {
                    return false;
                }
            }
            return true;
        }
    }

    /**
     * Extracts the delta (new content items) from incoming that aren't in existing. Assumes
     * isContentPrefix has already returned true.
     */
    public static List<Object> extractDelta(List<Object> existing, List<Object> incoming) {
        if (existing == null || existing.isEmpty()) {
            return incoming != null ? new ArrayList<>(incoming) : Collections.emptyList();
        }
        if (incoming == null || incoming.size() <= existing.size()) {
            return Collections.emptyList();
        }
        return new ArrayList<>(incoming.subList(existing.size(), incoming.size()));
    }

    /**
     * Creates a CreateEntryRequest with the specified content.
     * The epoch is NOT set on the request - it should be passed separately to appendAgentEntries.
     */
    public static CreateEntryRequest withContent(
            CreateEntryRequest original, List<Object> content) {
        CreateEntryRequest copy = new CreateEntryRequest();
        copy.setUserId(original != null ? original.getUserId() : null);
        copy.setChannel(CreateEntryRequest.ChannelEnum.MEMORY);
        copy.setContentType(original != null ? original.getContentType() : null);
        copy.setContent(content);
        return copy;
    }

    /**
     * Calculates the next epoch value. Initial epoch is 1.
     */
    public static Long nextEpoch(Long current) {
        return current == null ? 1L : current + 1;
    }

    // ============ Legacy methods for backward compatibility ============

    public static List<MessageContent> fromRequests(List<CreateEntryRequest> requests) {
        if (requests == null || requests.isEmpty()) {
            return Collections.emptyList();
        }
        List<MessageContent> result = new ArrayList<>(requests.size());
        for (CreateEntryRequest request : requests) {
            if (request == null) {
                continue;
            }
            result.add(MessageContent.fromRequest(request));
        }
        return result;
    }

    public static List<MessageContent> fromDtos(List<EntryDto> messages) {
        if (messages == null || messages.isEmpty()) {
            return Collections.emptyList();
        }
        List<MessageContent> result = new ArrayList<>(messages.size());
        for (EntryDto message : messages) {
            if (message == null) {
                continue;
            }
            result.add(MessageContent.fromDto(message));
        }
        return result;
    }

    public static boolean isPrefix(List<MessageContent> prefix, List<MessageContent> candidate) {
        if (prefix == null || candidate == null || prefix.size() > candidate.size()) {
            return false;
        }
        for (int i = 0; i < prefix.size(); i++) {
            if (!prefix.get(i).equals(candidate.get(i))) {
                return false;
            }
        }
        return true;
    }

    /**
     * Creates copies of CreateEntryRequests for the MEMORY channel.
     * Note: Epoch is NOT set on the requests - it should be passed separately to appendAgentEntries.
     */
    public static List<CreateEntryRequest> copyForMemoryChannel(
            List<CreateEntryRequest> originals) {
        if (originals == null || originals.isEmpty()) {
            return Collections.emptyList();
        }
        List<CreateEntryRequest> normalized = new ArrayList<>(originals.size());
        for (CreateEntryRequest original : originals) {
            CreateEntryRequest copy = new CreateEntryRequest();
            copy.setUserId(original != null ? original.getUserId() : null);
            copy.setChannel(CreateEntryRequest.ChannelEnum.MEMORY);
            copy.setContentType(original != null ? original.getContentType() : null);
            copy.setContent(original != null ? original.getContent() : null);
            normalized.add(copy);
        }
        return normalized;
    }

    public static final class MessageContent {

        private final String userId;
        private final List<Object> content;

        private MessageContent(String userId, List<Object> content) {
            this.userId = userId;
            if (content == null) {
                this.content = Collections.emptyList();
            } else {
                this.content = Collections.unmodifiableList(new ArrayList<>(content));
            }
        }

        static MessageContent fromRequest(CreateEntryRequest request) {
            List<Object> blocks = request.getContent();
            return new MessageContent(request.getUserId(), blocks);
        }

        static MessageContent fromDto(EntryDto message) {
            return new MessageContent(message.getUserId(), message.getContent());
        }

        @Override
        public boolean equals(Object o) {
            if (this == o) {
                return true;
            }
            if (o == null || getClass() != o.getClass()) {
                return false;
            }
            MessageContent that = (MessageContent) o;
            // For memory sync, we compare content only - userId is ignored for sync decisions
            // as it can vary between stored messages and incoming sync requests
            // Compare content using JSON serialization to handle Map type differences
            // (LinkedHashMap vs HashMap)
            // This ensures semantic equality regardless of the specific Map implementation used
            try {
                // Use JsonNode comparison to handle field order differences
                JsonNode thisNode = OBJECT_MAPPER.valueToTree(content);
                JsonNode thatNode = OBJECT_MAPPER.valueToTree(that.content);
                return thisNode.equals(thatNode);
            } catch (Exception e) {
                // Fallback to direct comparison if JSON conversion fails
                return Objects.equals(content, that.content);
            }
        }

        @Override
        public int hashCode() {
            return Objects.hash(userId, content);
        }
    }
}
