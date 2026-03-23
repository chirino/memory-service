package io.github.chirino.memory.subagent.runtime;

import static io.github.chirino.memory.security.SecurityHelper.bearerToken;
import static io.github.chirino.memory.security.SecurityHelper.principalName;

import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.databind.ObjectMapper;
import dev.langchain4j.agent.tool.P;
import dev.langchain4j.agent.tool.Tool;
import dev.langchain4j.agent.tool.ToolMemoryId;
import io.quarkus.security.identity.SecurityIdentity;
import jakarta.inject.Inject;
import org.jboss.logging.Logger;

public abstract class SubAgentTaskTool {

    private static final Logger LOG = Logger.getLogger(SubAgentTaskTool.class);

    @Inject SubAgentTaskManager taskManager;
    @Inject SecurityIdentity securityIdentity;
    @Inject ObjectMapper objectMapper;

    @Tool(
            """
            Send a message to a sub-agent conversation. When childConversationId is omitted, the
            runtime creates a new child conversation and sends the first delegated task. When
            childConversationId is present, the runtime appends the message to that child
            conversation and resumes the sub-agent. Joined task results are gathered before the
            parent turn is finalized. The tool returns a JSON object with the childConversationId.
            """)
    public String messageSubAgent(
            @ToolMemoryId String parentConversationId,
            @P(
                            value =
                                    "Optional child conversation id. Omit it to create a new"
                                            + " child conversation.",
                            required = false)
                    String childConversationId,
            @P("The task or message to send to the sub-agent.") String message) {
        try {
            return toJson(
                    taskManager.messageTask(
                            parentConversationId,
                            blankToNull(childConversationId),
                            message,
                            childAgentId(),
                            principalName(securityIdentity),
                            bearerToken(securityIdentity),
                            this::createExecution));
        } catch (RuntimeException e) {
            return errorJson(e);
        }
    }

    @Tool(
            """
            Get the current status of a sub-agent conversation. Use this only when you need to
            inspect the current state or retrieve the latest result. For streaming child tools,
            the response includes any response text streamed so far.
            """)
    public String getSubAgentStatus(
            @ToolMemoryId String parentConversationId,
            @P("The child conversation id returned by messageSubAgent.")
                    String childConversationId) {
        try {
            return toJson(taskManager.getStatus(parentConversationId, childConversationId));
        } catch (RuntimeException e) {
            return errorJson(e);
        }
    }

    private String toJson(Object value) {
        try {
            return objectMapper.writeValueAsString(value);
        } catch (JsonProcessingException e) {
            LOG.warn("Failed to serialize sub-agent tool response", e);
            return "{\"error\":\"Failed to serialize sub-agent tool response\"}";
        }
    }

    private static String blankToNull(String value) {
        return value == null || value.isBlank() ? null : value;
    }

    private static String escapeJson(String value) {
        return value.replace("\\", "\\\\").replace("\"", "\\\"");
    }

    private String errorJson(RuntimeException e) {
        LOG.warn("Sub-agent tool failed", e);
        String message = e.getMessage() == null ? e.getClass().getSimpleName() : e.getMessage();
        return "{\"error\":\"" + escapeJson(message) + "\"}";
    }

    protected SubAgentTaskExecution createExecution(SubAgentTaskRequest request) {
        return SubAgentTaskExecution.immediate(handleTask(request));
    }

    protected String childAgentId() {
        return getClass().getSimpleName().replaceFirst("Tool$", "");
    }

    protected String handleTask(SubAgentTaskRequest request) {
        throw new UnsupportedOperationException("handleTask is not implemented");
    }
}
