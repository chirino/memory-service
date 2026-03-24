package io.github.chirino.memory.subagent.runtime;

import com.fasterxml.jackson.annotation.JsonInclude;

@JsonInclude(JsonInclude.Include.NON_NULL)
public record SubAgentStatusTaskView(
        String taskId, SubAgentTaskStatus status, String response, String lastError) {

    static SubAgentStatusTaskView from(SubAgentTaskResult result) {
        String response =
                result.lastResponse() != null
                        ? result.lastResponse()
                        : result.streamedResponseSoFar();
        return new SubAgentStatusTaskView(
                result.childConversationId(), result.status(), response, result.lastError());
    }
}
