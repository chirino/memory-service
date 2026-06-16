package io.github.chirino.memory.history.runtime;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import jakarta.enterprise.context.ApplicationScoped;
import java.util.ArrayList;
import java.util.List;
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
    public List<AttachmentDescriptor> extract(String toolName, String result) {
        if (result == null || result.isBlank()) {
            return List.of();
        }

        List<AttachmentDescriptor> attachments = new ArrayList<>();
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

    private void extractFromNode(JsonNode node, List<AttachmentDescriptor> attachments) {
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
                String contentType =
                        node.has("contentType") ? node.get("contentType").asText() : null;
                String name = node.has("name") ? node.get("name").asText() : null;
                // Use explicit href if provided, otherwise derive from attachmentId
                String href =
                        node.has("href") ? node.get("href").asText() : "/v1/attachments/" + id;
                attachments.add(new AttachmentDescriptor(id, contentType, name, href));
            }
        }
    }
}
