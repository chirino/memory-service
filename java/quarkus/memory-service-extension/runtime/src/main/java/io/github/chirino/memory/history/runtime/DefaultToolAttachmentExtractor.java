package io.github.chirino.memory.history.runtime;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import jakarta.enterprise.context.ApplicationScoped;
import java.util.ArrayList;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import org.jboss.logging.Logger;

/**
 * Default implementation that parses tool output as JSON and looks for {@code attachmentId} fields.
 * Supports both single-object and array results.
 */
@ApplicationScoped
public class DefaultToolAttachmentExtractor implements ToolAttachmentExtractor {

    private static final Logger LOG = Logger.getLogger(DefaultToolAttachmentExtractor.class);
    private static final ObjectMapper MAPPER = new ObjectMapper();

    @Override
    public List<Map<String, Object>> extract(String toolName, String result) {
        if (result == null || result.isBlank()) {
            return List.of();
        }

        List<Map<String, Object>> attachments = new ArrayList<>();
        try {
            JsonNode root = MAPPER.readTree(result);
            LOG.debugf(
                    "Parsed tool output for tool=%s: nodeType=%s, length=%d",
                    toolName, root.getNodeType(), result.length());
            extractFromNode(root, attachments);
        } catch (Exception e) {
            LOG.debugf(
                    "Could not parse tool output as JSON for tool %s: %s",
                    toolName, e.getMessage());
        }
        LOG.debugf("Extracted %d attachments from tool=%s", attachments.size(), toolName);
        return attachments;
    }

    private void extractFromNode(JsonNode node, List<Map<String, Object>> attachments) {
        if (node == null) {
            return;
        }

        // Handle double-encoded JSON strings (e.g. LangChain4j wraps String tool results
        // in quotes, so resultText() returns "{"attachmentId":...}" as a JSON string).
        if (node.isTextual()) {
            try {
                JsonNode parsed = MAPPER.readTree(node.asText());
                if (parsed != null && !parsed.isTextual()) {
                    extractFromNode(parsed, attachments);
                }
            } catch (Exception e) {
                // Not JSON inside the string - ignore
            }
            return;
        }

        if (node.isArray()) {
            for (JsonNode element : node) {
                extractFromNode(element, attachments);
            }
            return;
        }

        if (node.isObject()) {
            JsonNode attachmentId = node.get("attachmentId");
            if (attachmentId != null && attachmentId.isTextual()) {
                String id = attachmentId.asText();
                Map<String, Object> att = new LinkedHashMap<>();
                att.put("attachmentId", id);
                if (node.has("contentType")) {
                    att.put("contentType", node.get("contentType").asText());
                }
                if (node.has("name")) {
                    att.put("name", node.get("name").asText());
                }
                // Use explicit href if provided, otherwise derive from attachmentId
                att.put(
                        "href",
                        node.has("href") ? node.get("href").asText() : "/v1/attachments/" + id);
                attachments.add(att);
            }
        }
    }
}
