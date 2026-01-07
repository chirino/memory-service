package example;

import com.fasterxml.jackson.databind.ObjectMapper;
import io.github.chirino.memory.client.api.ConversationsApi;
import io.github.chirino.memory.client.model.CreateSummaryRequest;
import io.github.chirino.memory.client.model.ListConversationMessages200Response;
import io.github.chirino.memory.client.model.Message;
import io.github.chirino.memory.client.model.MessageChannel;
import io.quarkus.security.identity.SecurityIdentity;
import io.quarkus.security.runtime.SecurityIdentityAssociation;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.ws.rs.POST;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.PathParam;
import jakarta.ws.rs.core.Response;
import java.time.OffsetDateTime;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.regex.Matcher;
import java.util.regex.Pattern;
import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.eclipse.microprofile.rest.client.inject.RestClient;
import org.jboss.logging.Logger;

/**
 * Summerizes and Redacts a conversation.
 *
 * This will update the conversation title and redact the conversation transcript so it can be vectorized
 * stored safely in the vector store.
 */
@Path("/v1/conversations/{conversationId}/summerize")
@ApplicationScoped
public class SummerizationResource {

    private static final Logger LOG = Logger.getLogger(SummerizationResource.class);
    private static final int PAGE_SIZE = 200;

    @Inject @RestClient ConversationsApi conversationsApi;

    @Inject RedactionAssistant redactionAssistant;

    @Inject ObjectMapper objectMapper;

    @Inject SecurityIdentity securityIdentity;

    @Inject SecurityIdentityAssociation securityIdentityAssociation;

    @ConfigProperty(name = "agent.summarization.title-max-chars", defaultValue = "20000")
    int titleMaxChars;

    @ConfigProperty(
            name = "agent.summarization.input-context-size",
            defaultValue = "" + (200 * 1204))
    int inputContextSize;

    @POST
    public Response summerize(@PathParam("conversationId") String conversationId) {
        try {

            propagateSecurityIdentity();
            List<Message> historyMessages = fetchHistoryMessages(conversationId);
            if (historyMessages.isEmpty()) {
                return Response.noContent().build();
            }

            String transcript = buildTranscript(historyMessages);
            if (transcript.isBlank()) {
                return Response.noContent().build();
            }

            Message last = historyMessages.get(historyMessages.size() - 1);
            OffsetDateTime summarizedAt =
                    last.getCreatedAt() != null ? last.getCreatedAt() : OffsetDateTime.now();

            RedactionPayload redactionPayload = analyzeRedactionsInChunks(transcript);
            LOG.infof(
                    "redactionPayload, title: %s, redactions: %d",
                    redactionPayload.title, redactionPayload.redact.size());

            // Apply redactions to the transcript to create the summary
            String redactedTranscript = applyRedactions(transcript, redactionPayload.redact);
            LOG.infof("redactedTranscript: %s", redactedTranscript);

            CreateSummaryRequest request = new CreateSummaryRequest();
            request.setTitle(redactionPayload.title());
            request.setSummary(redactedTranscript);
            request.setUntilMessageId(last.getId());
            request.setSummarizedAt(summarizedAt);
            conversationsApi.createConversationSummary(conversationId, request);
            return Response.status(Response.Status.CREATED).build();
        } catch (Exception e) {
            LOG.errorf(e, "Failed to summarize conversationId=%s", conversationId);
            return Response.serverError().build();
        }
    }

    private List<Message> fetchHistoryMessages(String conversationId) {
        List<Message> all = new ArrayList<>();
        String cursor = null;
        while (true) {
            ListConversationMessages200Response response =
                    conversationsApi.listConversationMessages(
                            conversationId, cursor, PAGE_SIZE, MessageChannel.HISTORY);
            List<Message> data = response != null ? response.getData() : null;
            if (data != null && !data.isEmpty()) {
                all.addAll(data);
            }
            String next = response != null ? response.getNextCursor() : null;
            if (next == null || next.isBlank()) {
                break;
            }
            cursor = next;
        }
        return all;
    }

    private RedactionPayload analyzeRedactions(String transcript) throws Exception {
        String raw = extractJson(redactionAssistant.redact("", transcript, titleMaxChars));
        LOG.infof("summaryGenerator returned: %s", raw);
        RedactionPayload payload = objectMapper.readValue(raw, RedactionPayload.class);
        if (payload == null || payload.title() == null || payload.title().isBlank()) {
            throw new IllegalStateException("Summary generator returned an empty title");
        }
        if (payload.redact() == null) {
            throw new IllegalStateException("Summary generator returned null redact map");
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

    private String buildTranscript(List<Message> messages) {
        StringBuilder builder = new StringBuilder();
        for (Message message : messages) {
            if (message == null) {
                continue;
            }
            for (Object block : message.getContent()) {
                if (block == null) {
                    continue;
                }
                String text = extractText(List.of(block));
                if (text == null || text.isBlank()) {
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
                if (normalized.equals("user") || normalized.equals("human")) {
                    return "User:\n\n";
                }
                if (normalized.equals("assistant") || normalized.equals("ai")) {
                    return "AI:\n\n";
                }
                if (normalized.equals("system")) {
                    return "System:\n\n";
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
            if (block == null) {
                continue;
            }
            if (block instanceof Map<?, ?> map) {
                Object text = map.get("text");
                if (text instanceof String s && !s.isBlank()) {
                    builder.append(s).append(' ');
                }
            } else if (block instanceof String s && !s.isBlank()) {
                builder.append(s).append(' ');
            }
        }
        return builder.toString().trim();
    }

    private record RedactionPayload(String title, Map<String, String> redact) {}

    /**
     * Ensures the SecurityIdentity is propagated to the REST client context
     * so that GlobalTokenPropagationFilter can access it.
     */
    private void propagateSecurityIdentity() {
        if (securityIdentity != null && securityIdentityAssociation != null) {
            securityIdentityAssociation.setIdentity(securityIdentity);
        }
    }
}
