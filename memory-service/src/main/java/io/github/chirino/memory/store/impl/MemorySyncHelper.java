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

    public static List<CreateEntryRequest> withEpoch(
            List<CreateEntryRequest> originals, Long epoch) {
        if (originals == null || originals.isEmpty()) {
            return Collections.emptyList();
        }
        List<CreateEntryRequest> normalized = new ArrayList<>(originals.size());
        for (CreateEntryRequest original : originals) {
            CreateEntryRequest copy = new CreateEntryRequest();
            copy.setUserId(original != null ? original.getUserId() : null);
            copy.setChannel(CreateEntryRequest.ChannelEnum.MEMORY);
            copy.setEpoch(epoch);
            copy.setContentType(original != null ? original.getContentType() : null);
            copy.setContent(original != null ? original.getContent() : null);
            normalized.add(copy);
        }
        return normalized;
    }

    public static Long nextEpoch(Long current) {
        return current == null ? 1L : current + 1;
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
