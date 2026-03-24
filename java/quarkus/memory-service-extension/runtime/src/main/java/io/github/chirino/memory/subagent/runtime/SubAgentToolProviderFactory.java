package io.github.chirino.memory.subagent.runtime;

import static io.github.chirino.memory.security.SecurityHelper.bearerToken;
import static io.github.chirino.memory.security.SecurityHelper.principalName;

import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import dev.langchain4j.agent.tool.ToolExecutionRequest;
import dev.langchain4j.agent.tool.ToolSpecification;
import dev.langchain4j.model.chat.request.json.JsonArraySchema;
import dev.langchain4j.model.chat.request.json.JsonObjectSchema;
import dev.langchain4j.model.chat.request.json.JsonStringSchema;
import dev.langchain4j.service.tool.ToolExecutor;
import dev.langchain4j.service.tool.ToolProvider;
import dev.langchain4j.service.tool.ToolProviderResult;
import io.quarkiverse.langchain4j.runtime.aiservice.ChatEvent;
import io.quarkus.arc.Arc;
import io.quarkus.security.identity.SecurityIdentity;
import io.smallrye.mutiny.Multi;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.enterprise.inject.Instance;
import jakarta.inject.Inject;
import java.util.ArrayList;
import java.util.List;
import java.util.Objects;
import org.jboss.logging.Logger;

@ApplicationScoped
public class SubAgentToolProviderFactory {

    private static final Logger LOG = Logger.getLogger(SubAgentToolProviderFactory.class);
    static final int DEFAULT_WAIT_SECONDS = 5;

    @Inject SubAgentTaskManager taskManager;
    @Inject ObjectMapper objectMapper;
    @Inject Instance<SecurityIdentity> securityIdentityInstance;

    public Builder builder() {
        return new Builder(this);
    }

    private ToolProvider createStreamingProvider(
            SubAgentToolDefinition definition, StreamingInvoker invoker) {
        Objects.requireNonNull(invoker, "invoker");
        return createProvider(
                definition,
                request -> SubAgentTaskExecution.streaming(invoker.handleTaskStream(request)));
    }

    private ToolProvider createProvider(
            SubAgentToolDefinition definition, SubAgentTaskInvoker invoker) {
        Objects.requireNonNull(definition, "definition");
        Objects.requireNonNull(invoker, "invoker");
        ToolSpecification messageSpec = messageToolSpecification(definition);
        ToolSpecification statusSpec = statusToolSpecification(definition);
        ToolSpecification waitSpec = waitToolSpecification(definition);
        ToolSpecification stopSpec = stopToolSpecification(definition);
        ToolExecutor messageExecutor =
                (request, memoryId) -> messageSubAgent(definition, invoker, request, memoryId);
        ToolExecutor statusExecutor = (request, memoryId) -> getSubAgentStatus(request, memoryId);
        ToolExecutor waitExecutor = (request, memoryId) -> waitSubAgent(request, memoryId);
        ToolExecutor stopExecutor = (request, memoryId) -> stopSubAgent(request, memoryId);
        return ignored ->
                ToolProviderResult.builder()
                        .add(messageSpec, messageExecutor)
                        .add(statusSpec, statusExecutor)
                        .add(waitSpec, waitExecutor)
                        .add(stopSpec, stopExecutor)
                        .build();
    }

    private String messageSubAgent(
            SubAgentToolDefinition definition,
            SubAgentTaskInvoker invoker,
            ToolExecutionRequest request,
            Object memoryId) {
        try {
            JsonNode arguments = arguments(request);
            String parentConversationId = requireConversationId(memoryId);
            String childConversationId = textValue(arguments, "taskId");
            String childAgentId = textValue(arguments, "agentId");
            String message = requiredTextValue(arguments, "message");
            String mode = textValue(arguments, "mode");
            return toJson(
                    SubAgentStartTaskView.from(
                            taskManager.messageTask(
                                    parentConversationId,
                                    childConversationId,
                                    message,
                                    mode,
                                    childAgentId != null ? childAgentId : definition.childAgentId(),
                                    definition.maxConcurrency(),
                                    resolveUserId(),
                                    resolveBearerToken(),
                                    invoker)));
        } catch (RuntimeException e) {
            return errorJson(e);
        }
    }

    private String getSubAgentStatus(ToolExecutionRequest request, Object memoryId) {
        try {
            JsonNode arguments = arguments(request);
            return toJson(
                    SubAgentStatusTaskView.from(
                            taskManager.getStatus(
                                    requireConversationId(memoryId),
                                    requiredTextValue(arguments, "taskId"),
                                    resolveBearerToken())));
        } catch (RuntimeException e) {
            return errorJson(e);
        }
    }

    private String waitSubAgent(ToolExecutionRequest request, Object memoryId) {
        try {
            JsonNode arguments = arguments(request);
            JsonNode waitNode = arguments.get("secs");
            int maxWaitSeconds =
                    normalizeWaitSeconds(
                            waitNode == null || waitNode.isNull() ? 0 : waitNode.asInt(0));
            String parentConversationId = requireConversationId(memoryId);
            return toJson(
                    simplify(
                            taskManager.waitForTasks(
                                    parentConversationId,
                                    stringListValue(arguments, "taskIds"),
                                    maxWaitSeconds,
                                    resolveBearerToken())));
        } catch (RuntimeException e) {
            return errorJson(e);
        }
    }

    static int normalizeWaitSeconds(int seconds) {
        return seconds <= 0 ? DEFAULT_WAIT_SECONDS : seconds;
    }

    static List<SubAgentWaitTaskView> simplify(SubAgentWaitResult result) {
        List<SubAgentWaitTaskView> tasks = new ArrayList<>();
        for (SubAgentTaskResult task : result.tasks()) {
            tasks.add(SubAgentWaitTaskView.from(task));
        }
        return tasks;
    }

    private String stopSubAgent(ToolExecutionRequest request, Object memoryId) {
        try {
            JsonNode arguments = arguments(request);
            return toJson(
                    SubAgentStartTaskView.from(
                            taskManager.stopTask(
                                    requireConversationId(memoryId),
                                    requiredTextValue(arguments, "taskId"),
                                    resolveBearerToken())));
        } catch (RuntimeException e) {
            return errorJson(e);
        }
    }

    private JsonNode arguments(ToolExecutionRequest request) {
        try {
            return objectMapper.readTree(request.arguments());
        } catch (JsonProcessingException e) {
            throw new IllegalArgumentException("Failed to parse tool arguments", e);
        }
    }

    private static String textValue(JsonNode arguments, String fieldName) {
        JsonNode value = arguments.get(fieldName);
        if (value == null || value.isNull()) {
            return null;
        }
        String text = value.asText();
        return text == null || text.isBlank() ? null : text;
    }

    private static String requiredTextValue(JsonNode arguments, String fieldName) {
        String value = textValue(arguments, fieldName);
        if (value == null) {
            throw new IllegalArgumentException(fieldName + " is required");
        }
        return value;
    }

    private static List<String> stringListValue(JsonNode arguments, String fieldName) {
        JsonNode value = arguments.get(fieldName);
        if (value == null || value.isNull()) {
            return List.of();
        }
        if (!value.isArray()) {
            throw new IllegalArgumentException(fieldName + " must be an array of strings");
        }
        List<String> results = new ArrayList<>();
        for (JsonNode item : value) {
            if (item == null || item.isNull()) {
                continue;
            }
            String text = item.asText();
            if (text != null && !text.isBlank()) {
                results.add(text);
            }
        }
        return results;
    }

    private static String requireConversationId(Object memoryId) {
        if (memoryId == null) {
            throw new IllegalArgumentException("Conversation id is required");
        }
        return memoryId.toString();
    }

    private String toJson(Object value) {
        try {
            return objectMapper.writeValueAsString(value);
        } catch (JsonProcessingException e) {
            LOG.warn("Failed to serialize sub-agent tool response", e);
            return "{\"error\":\"Failed to serialize sub-agent tool response\"}";
        }
    }

    private static String escapeJson(String value) {
        return value.replace("\\", "\\\\").replace("\"", "\\\"");
    }

    private String errorJson(RuntimeException e) {
        LOG.warn("Sub-agent tool failed", e);
        String message = e.getMessage() == null ? e.getClass().getSimpleName() : e.getMessage();
        return "{\"error\":\"" + escapeJson(message) + "\"}";
    }

    private String resolveUserId() {
        SubAgentExecutionContext.State state = SubAgentExecutionContext.current();
        if (state != null && state.userId() != null && !state.userId().isBlank()) {
            return state.userId();
        }
        return principalName(resolveSecurityIdentity());
    }

    private String resolveBearerToken() {
        SubAgentExecutionContext.State state = SubAgentExecutionContext.current();
        if (state != null && state.bearerToken() != null && !state.bearerToken().isBlank()) {
            return state.bearerToken();
        }
        return bearerToken(resolveSecurityIdentity());
    }

    private SecurityIdentity resolveSecurityIdentity() {
        if (!Arc.container().requestContext().isActive()) {
            return null;
        }
        return securityIdentityInstance.isResolvable() ? securityIdentityInstance.get() : null;
    }

    static String messageToolDescription(SubAgentToolDefinition definition) {
        String description = definition.messageToolDescription();
        if (definition.maxConcurrency() == null) {
            return description;
        }
        return description
                + " At most "
                + definition.maxConcurrency()
                + " tasks may be RUNNING at the same time for one parent conversation. Starting"
                + " another task beyond that limit returns an error.";
    }

    private static ToolSpecification messageToolSpecification(SubAgentToolDefinition definition) {
        return ToolSpecification.builder()
                .name(definition.messageToolName())
                .description(messageToolDescription(definition))
                .parameters(
                        JsonObjectSchema.builder()
                                .addStringProperty(
                                        "taskId",
                                        "Optional delegated agent conversation id. Omit it to"
                                                + " create a new child agent conversation.")
                                .addStringProperty(
                                        "agentId",
                                        "Optional child agent id override for this agent"
                                                + " conversation.")
                                .addStringProperty(
                                        "message",
                                        "The work request or follow-up message to send to the"
                                                + " child agent.")
                                .addEnumProperty(
                                        "mode",
                                        List.of("queue", "interrupt"),
                                        "Required when taskId is provided. queue replaces any"
                                            + " queued follow-up; interrupt stops the current run"
                                            + " and restarts with the new message.")
                                .required("message")
                                .build())
                .build();
    }

    private static ToolSpecification statusToolSpecification(SubAgentToolDefinition definition) {
        return ToolSpecification.builder()
                .name(definition.statusToolName())
                .description(definition.statusToolDescription())
                .parameters(
                        JsonObjectSchema.builder()
                                .addStringProperty(
                                        "taskId",
                                        "The delegated agent conversation id returned by the"
                                                + " agentSend tool.")
                                .required("taskId")
                                .build())
                .build();
    }

    private static ToolSpecification waitToolSpecification(SubAgentToolDefinition definition) {
        return ToolSpecification.builder()
                .name(definition.waitToolName())
                .description(definition.waitToolDescription())
                .parameters(
                        JsonObjectSchema.builder()
                                .addProperty(
                                        "taskIds",
                                        JsonArraySchema.builder()
                                                .description(
                                                        "Optional list of task ids"
                                                                + " to wait for. Omit it or pass"
                                                                + " an empty list to wait for all"
                                                                + " current child tasks of the"
                                                                + " parent conversation.")
                                                .items(
                                                        JsonStringSchema.builder()
                                                                .description(
                                                                        "A task id returned by"
                                                                                + " the agentSend"
                                                                                + " tool.")
                                                                .build())
                                                .build())
                                .addIntegerProperty(
                                        "secs",
                                        "Maximum number of seconds to wait before returning the"
                                                + " current status.")
                                .build())
                .build();
    }

    private static ToolSpecification stopToolSpecification(SubAgentToolDefinition definition) {
        return ToolSpecification.builder()
                .name(definition.stopToolName())
                .description(definition.stopToolDescription())
                .parameters(
                        JsonObjectSchema.builder()
                                .addStringProperty(
                                        "taskId",
                                        "The delegated agent conversation id returned by the"
                                                + " agentSend tool.")
                                .required("taskId")
                                .build())
                .build();
    }

    @FunctionalInterface
    public interface StreamingInvoker {
        Multi<ChatEvent> handleTaskStream(SubAgentTaskRequest request);
    }

    public static final class Builder {
        private final SubAgentToolProviderFactory factory;
        private SubAgentToolDefinition.Builder definition = SubAgentToolDefinition.builder();

        private Builder(SubAgentToolProviderFactory factory) {
            this.factory = factory;
        }

        public Builder definition(SubAgentToolDefinition definition) {
            this.definition =
                    SubAgentToolDefinition.builder()
                            .childAgentId(definition.childAgentId())
                            .maxConcurrency(definition.maxConcurrency())
                            .messageToolName(definition.messageToolName())
                            .messageToolDescription(definition.messageToolDescription())
                            .statusToolName(definition.statusToolName())
                            .statusToolDescription(definition.statusToolDescription())
                            .waitToolName(definition.waitToolName())
                            .waitToolDescription(definition.waitToolDescription())
                            .stopToolName(definition.stopToolName())
                            .stopToolDescription(definition.stopToolDescription());
            return this;
        }

        public Builder childAgentId(String childAgentId) {
            definition.childAgentId(childAgentId);
            return this;
        }

        public Builder maxConcurrency(int maxConcurrency) {
            definition.maxConcurrency(maxConcurrency);
            return this;
        }

        public Builder messageToolName(String messageToolName) {
            definition.messageToolName(messageToolName);
            return this;
        }

        public Builder messageToolDescription(String messageToolDescription) {
            definition.messageToolDescription(messageToolDescription);
            return this;
        }

        public Builder statusToolName(String statusToolName) {
            definition.statusToolName(statusToolName);
            return this;
        }

        public Builder statusToolDescription(String statusToolDescription) {
            definition.statusToolDescription(statusToolDescription);
            return this;
        }

        public Builder waitToolName(String waitToolName) {
            definition.waitToolName(waitToolName);
            return this;
        }

        public Builder waitToolDescription(String waitToolDescription) {
            definition.waitToolDescription(waitToolDescription);
            return this;
        }

        public Builder stopToolName(String stopToolName) {
            definition.stopToolName(stopToolName);
            return this;
        }

        public Builder stopToolDescription(String stopToolDescription) {
            definition.stopToolDescription(stopToolDescription);
            return this;
        }

        public ToolProvider createStreamingProvider(StreamingInvoker invoker) {
            return factory.createStreamingProvider(definition.build(), invoker);
        }

        public ToolProvider createProvider(SubAgentTaskInvoker invoker) {
            return factory.createProvider(definition.build(), invoker);
        }
    }
}
