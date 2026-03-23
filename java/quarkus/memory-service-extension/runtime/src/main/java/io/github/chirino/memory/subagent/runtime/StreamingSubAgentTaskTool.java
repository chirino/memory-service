package io.github.chirino.memory.subagent.runtime;

import io.quarkiverse.langchain4j.runtime.aiservice.ChatEvent;
import io.smallrye.mutiny.Multi;

public abstract class StreamingSubAgentTaskTool extends SubAgentTaskTool {

    @Override
    protected final SubAgentTaskExecution createExecution(SubAgentTaskRequest request) {
        return SubAgentTaskExecution.streaming(handleTaskStream(request));
    }

    protected abstract Multi<ChatEvent> handleTaskStream(SubAgentTaskRequest request);
}
