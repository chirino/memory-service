package org.acme;

import dev.langchain4j.rag.content.Content;
import dev.langchain4j.rag.content.retriever.ContentRetriever;
import dev.langchain4j.rag.query.Query;
import io.github.chirino.memory.client.api.MemoriesApi;
import io.github.chirino.memory.client.model.MemoryItem;
import io.github.chirino.memory.client.model.SearchMemoriesRequest;
import io.github.chirino.memory.client.model.SearchMemoriesResponse;
import io.quarkus.logging.Log;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import java.util.ArrayList;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Map;
import java.util.Set;

@ApplicationScoped
public class CognitionMemoryContentRetriever implements ContentRetriever {

    @Inject CognitionMemoryRagConfig config;
    @Inject CognitionMemoryAccess access;

    @Override
    public List<Content> retrieve(Query query) {
        if (!config.enabled() || !config.additionalSearch().enabled()) {
            return List.of();
        }
        String text = query == null ? null : query.text();
        if (text == null || text.strip().length() < config.minQueryChars()) {
            return List.of();
        }

        try {
            String userId = access.userId();
            MemoriesApi api = access.memoriesApi();

            SearchMemoriesRequest request = new SearchMemoriesRequest();
            request.setNamespacePrefix(access.cognitionPrefix(userId));
            request.setQuery(text);
            request.setLimit(config.limit());
            request.setIncludeUsage(config.includeUsage());
            request.setArchived(SearchMemoriesRequest.ArchivedEnum.EXCLUDE);

            SearchMemoriesResponse response = api.searchMemories(request);
            return toContents(response == null ? null : response.getItems());
        } catch (RuntimeException e) {
            return retrievalFailure("ad hoc cognition memory search", e);
        }
    }

    private List<Content> toContents(List<MemoryItem> items) {
        if (items == null || items.isEmpty()) {
            return List.of();
        }
        List<Content> contents = new ArrayList<>();
        Set<String> seenContent = new LinkedHashSet<>();
        int chars = 0;
        for (MemoryItem item : items) {
            if (!adHocCandidate(item, config.minSemanticScore())) {
                continue;
            }
            String content = CognitionMemoryValues.supportedContent(item);
            if (content == null || !seenContent.add(content)) {
                continue;
            }
            String formatted = formatMemory(item, content);
            if (chars + formatted.length() > config.maxChars()) {
                int remaining = config.maxChars() - chars;
                if (remaining <= 0) {
                    break;
                }
                formatted = formatted.substring(0, Math.max(0, remaining));
            }
            contents.add(Content.from(formatted));
            chars += formatted.length();
        }
        return contents;
    }

    static boolean adHocCandidate(MemoryItem item, double minSemanticScore) {
        if (item == null || item.getScore() == null || item.getScore() < minSemanticScore) {
            return false;
        }
        Map<String, Object> value = item.getValue();
        String kind = CognitionMemoryValues.stringValue(value, "kind");
        return !CognitionMemoryValues.namespaceEndsWith(item.getNamespace(), "profile_context")
                && !CognitionMemoryValues.namespaceEndsWith(item.getNamespace(), "profile_input")
                && !"profile_context".equals(kind)
                && !"profile_context_snapshot".equals(kind)
                && !"profile_context_inputs".equals(kind);
    }

    private String formatMemory(MemoryItem item, String content) {
        String kind = CognitionMemoryValues.stringValue(item.getValue(), "kind");
        if (kind == null && item.getNamespace() != null && !item.getNamespace().isEmpty()) {
            kind = item.getNamespace().get(item.getNamespace().size() - 1);
        }
        String confidence = scalarString(item.getValue(), "confidence");
        String source = compactSource(item);

        StringBuilder builder = new StringBuilder();
        builder.append("Durable user memory\n");
        if (kind != null) {
            builder.append("kind: ").append(kind).append('\n');
        }
        if (confidence != null) {
            builder.append("confidence: ").append(confidence).append('\n');
        }
        builder.append("memory: ").append(content.strip()).append('\n');
        if (source != null) {
            builder.append("source: ").append(source).append('\n');
        }
        return builder.toString().strip();
    }

    private static String scalarString(Map<String, Object> value, String key) {
        Object item = value == null ? null : value.get(key);
        if (item instanceof Number || item instanceof Boolean) {
            return item.toString();
        }
        return item instanceof String text && !text.isBlank() ? text : null;
    }

    @SuppressWarnings("unchecked")
    private static String compactSource(MemoryItem item) {
        Map<String, Object> value = item.getValue();
        Object provenance = value == null ? null : value.get("provenance");
        if (provenance instanceof Map<?, ?> raw) {
            Map<String, Object> map = (Map<String, Object>) raw;
            String conversationId = scalarString(map, "conversation_id");
            if (conversationId != null) {
                return "conversation " + conversationId;
            }
            Object conversationIds = map.get("conversation_ids");
            if (conversationIds instanceof List<?> ids && !ids.isEmpty()) {
                return "conversation " + ids.get(0);
            }
        }
        return item.getKey() == null ? null : "memory " + item.getKey();
    }

    private List<Content> retrievalFailure(String operation, RuntimeException e) {
        if (config.failOpen()) {
            Log.warnf(e, "Cognition memory retrieval failed during %s", operation);
            return List.of();
        }
        throw e;
    }
}
