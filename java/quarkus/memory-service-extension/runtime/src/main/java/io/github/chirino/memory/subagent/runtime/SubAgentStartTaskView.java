package io.github.chirino.memory.subagent.runtime;

public record SubAgentStartTaskView(String taskId, SubAgentTaskStatus status) {

    static SubAgentStartTaskView from(SubAgentTaskResult result) {
        return new SubAgentStartTaskView(result.childConversationId(), result.status());
    }
}
