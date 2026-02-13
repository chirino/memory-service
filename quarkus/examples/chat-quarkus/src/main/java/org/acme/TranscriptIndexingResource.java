package org.acme;

import static io.github.chirino.memory.security.SecurityHelper.bearerToken;

import com.fasterxml.jackson.databind.ObjectMapper;
import io.github.chirino.memory.client.api.ConversationsApi;
import io.github.chirino.memory.client.api.SearchApi;
import io.github.chirino.memory.client.model.Channel;
import io.github.chirino.memory.client.model.Entry;
import io.github.chirino.memory.client.model.IndexEntryRequest;
import io.github.chirino.memory.client.model.ListConversationEntries200Response;
import io.github.chirino.memory.runtime.MemoryServiceApiBuilder;
import io.quarkus.security.identity.SecurityIdentity;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.ws.rs.POST;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.PathParam;
import jakarta.ws.rs.core.Response;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.UUID;
import java.util.regex.Matcher;
import java.util.regex.Pattern;
import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.jboss.logging.Logger;

/**
 * Redacts and Indexes and a conversation transcript.
 *
 * <p>This will update the conversation title and redact the conversation transcript so it can be
 * indexed by the memory-service.
 */
@Path("/v1/conversations/{conversationId}/index")
@ApplicationScoped
public class TranscriptIndexingResource {

    private static final Logger LOG = Logger.getLogger(TranscriptIndexingResource.class);
    private static final int PAGE_SIZE = 200;

    @Inject MemoryServiceApiBuilder memoryServiceApiBuilder;

    @Inject RedactionAssistant redactionAssistant;

    @Inject ObjectMapper objectMapper;

    @Inject SecurityIdentity securityIdentity;

    @ConfigProperty(name = "agent.indexing.title-max-chars", defaultValue = "20000")
    int titleMaxChars;

    @ConfigProperty(name = "agent.indexing.input-context-size", defaultValue = "" + (200 * 1204))
    int inputContextSize;

    @POST
    public Response indexTranscript(@PathParam("conversationId") String conversationId) {
        try {

            List<Entry> historyEntries = fetchHistoryEntries(conversationId);
            if (historyEntries.isEmpty()) {
                return Response.noContent().build();
            }

            String transcript = buildTranscript(historyEntries);
            if (transcript.isBlank()) {
                return Response.noContent().build();
            }

            Entry last = historyEntries.get(historyEntries.size() - 1);

            RedactionPayload redactionPayload = analyzeRedactionsInChunks(transcript);
            LOG.infof(
                    "redactionPayload, title: %s, redactions: %d",
                    redactionPayload.title, redactionPayload.redact.size());

            // Apply redactions to the transcript
            String redactedTranscript = applyRedactions(transcript, redactionPayload.redact);
            LOG.infof("redactedTranscript: %s", redactedTranscript);

            // Index the redacted transcript for the last entry
            IndexEntryRequest request = new IndexEntryRequest();
            request.setConversationId(UUID.fromString(conversationId));
            request.setEntryId(last.getId());
            request.setIndexedContent(redactedTranscript);
            searchApi().indexConversations(List.of(request));
            return Response.status(Response.Status.CREATED).build();
        } catch (Exception e) {
            LOG.errorf(e, "Failed to index transcript for conversationId=%s", conversationId);
            return Response.serverError().build();
        }
    }

    private List<Entry> fetchHistoryEntries(String conversationId) {
        List<Entry> all = new ArrayList<>();
        UUID cursor = null;
        while (true) {
            ListConversationEntries200Response response =
                    conversationsApi()
                            .listConversationEntries(
                                    UUID.fromString(conversationId),
                                    cursor,
                                    PAGE_SIZE,
                                    Channel.HISTORY,
                                    null,
                                    null);
            List<Entry> data = response != null ? response.getData() : null;
            if (data != null && !data.isEmpty()) {
                all.addAll(data);
            }
            String next = response != null ? response.getNextCursor() : null;
            if (next == null || next.isBlank()) {
                break;
            }
            cursor = UUID.fromString(next);
        }
        return all;
    }

    private RedactionPayload analyzeRedactions(String transcript) throws Exception {
        String raw = extractJson(redactionAssistant.redact("", transcript, titleMaxChars));
        LOG.infof("redactionAssistant returned: %s", raw);
        RedactionPayload payload = objectMapper.readValue(raw, RedactionPayload.class);
        if (payload == null || payload.title() == null || payload.title().isBlank()) {
            throw new IllegalStateException("Redaction assistant returned an empty title");
        }
        if (payload.redact() == null) {
            throw new IllegalStateException("Redaction assistant returned null redact map");
        }
        return payload;
    }

    private RedactionPayload analyzeRedactionsInChunks(String transcript) throws Exception {
        List<String> chunks = splitIntoChunks(transcript, inputContextSize);
        if (chunks.isEmpty()) {
            throw new IllegalStateException("No chunks to analyze");
        }

        // Analyze the first chunk
        RedactionPayload currentRedactions = analyzeRedactions(chunks.get(0));
        String currentTitle = currentRedactions.title();

        // For each subsequent chunk, analyze it and merge the redaction maps
        for (int i = 1; i < chunks.size(); i++) {
            RedactionPayload chunkRedactions = analyzeRedactions(chunks.get(i));
            // Merge redaction maps (later chunks may have additional redactions)
            Map<String, String> merged = new LinkedHashMap<>(currentRedactions.redact());
            merged.putAll(chunkRedactions.redact());
            currentRedactions = new RedactionPayload(currentTitle, merged);
        }

        return currentRedactions;
    }

    private List<String> splitIntoChunks(String text, int chunkSize) {
        List<String> chunks = new ArrayList<>();
        int start = 0;
        while (start < text.length()) {
            int end = Math.min(start + chunkSize, text.length());
            chunks.add(text.substring(start, end));
            start = end;
        }
        return chunks;
    }

    private String applyRedactions(String transcript, Map<String, String> redactions) {
        if (redactions == null || redactions.isEmpty()) {
            return transcript;
        }

        String result = transcript;
        // Sort by length (longest first) to avoid partial matches
        List<Map.Entry<String, String>> sorted =
                redactions.entrySet().stream()
                        .sorted((a, b) -> Integer.compare(b.getKey().length(), a.getKey().length()))
                        .toList();

        for (Map.Entry<String, String> entry : sorted) {
            String toRedact = entry.getKey();
            String reason = entry.getValue();
            String replacement = "[REDACTED: " + reason + "]";
            // Replace all occurrences (case-insensitive)
            Pattern pattern = Pattern.compile(Pattern.quote(toRedact), Pattern.CASE_INSENSITIVE);
            Matcher matcher = pattern.matcher(result);
            result = matcher.replaceAll(Matcher.quoteReplacement(replacement));
        }

        return result;
    }

    private String extractJson(String raw) {
        if (raw == null) {
            return "";
        }
        String trimmed = raw.trim();
        if (trimmed.startsWith("```")) {
            int firstLineEnd = trimmed.indexOf('\n');
            if (firstLineEnd >= 0) {
                trimmed = trimmed.substring(firstLineEnd + 1);
            }
            int fenceEnd = trimmed.lastIndexOf("```");
            if (fenceEnd >= 0) {
                trimmed = trimmed.substring(0, fenceEnd);
            }
            trimmed = trimmed.trim();
        }
        int start = trimmed.indexOf('{');
        int end = trimmed.lastIndexOf('}');
        if (start >= 0 && end > start) {
            return trimmed.substring(start, end + 1).trim();
        }
        return trimmed;
    }

    private String buildTranscript(List<Entry> entries) {
        StringBuilder builder = new StringBuilder();
        for (Entry entry : entries) {
            if (entry == null) {
                continue;
            }
            for (Object block : entry.getContent()) {
                if (block == null) {
                    continue;
                }
                String text = extractText(List.of(block));
                if (text.isBlank()) {
                    continue;
                }

                if (!builder.isEmpty()) {
                    builder.append("---\n");
                }
                builder.append(extractRolePrefix(block));
                builder.append(text.trim()).append("\n");
            }
        }
        return builder.toString().trim();
    }

    private String extractRolePrefix(Object block) {
        if (block instanceof Map<?, ?> map) {
            String role = extractString(map, "role");
            if (role == null) {
                role = extractString(map, "type");
            }
            if (role != null) {
                String normalized = role.trim().toLowerCase();
                switch (normalized) {
                    case "user", "human" -> {
                        return "User:\n\n";
                    }
                    case "assistant", "ai" -> {
                        return "AI:\n\n";
                    }
                    case "system" -> {
                        return "System:\n\n";
                    }
                    default -> {}
                }
            }
        }
        return "Message:\n\n";
    }

    private String extractString(Map<?, ?> map, String key) {
        Object value = map.get(key);
        return value instanceof String s ? s : null;
    }

    private String extractText(List<Object> content) {
        if (content == null || content.isEmpty()) {
            return "";
        }
        StringBuilder builder = new StringBuilder();
        for (Object block : content) {
            switch (block) {
                case Map<?, ?> map -> {
                    Object text = map.get("text");
                    if (text instanceof String s && !s.isBlank()) {
                        builder.append(s).append(' ');
                    }
                }
                case String s when !s.isBlank() -> builder.append(s).append(' ');
                case null, default -> {}
            }
        }
        return builder.toString().trim();
    }

    private record RedactionPayload(String title, Map<String, String> redact) {}

    private ConversationsApi conversationsApi() {
        String bearerToken = bearerToken(securityIdentity);
        return memoryServiceApiBuilder.withBearerAuth(bearerToken).build(ConversationsApi.class);
    }

    private SearchApi searchApi() {
        String bearerToken = bearerToken(securityIdentity);
        return memoryServiceApiBuilder.withBearerAuth(bearerToken).build(SearchApi.class);
    }
}
