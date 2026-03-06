package io.github.chirino.memoryservice.history;

import java.util.List;
import java.util.Map;

/**
 * Extracts attachment references from tool execution results. Implementations parse the tool output
 * and return any attachment metadata found.
 */
public interface ToolAttachmentExtractor {

    /**
     * Extract attachment references from a tool execution result.
     *
     * @param toolName the name of the tool that was executed
     * @param result the tool execution output (typically JSON)
     * @return list of attachment metadata maps (each containing at least "attachmentId")
     */
    List<Map<String, Object>> extract(String toolName, String result);
}
