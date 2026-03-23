package io.github.chirino.memory.subagent.runtime;

import io.quarkiverse.langchain4j.runtime.aiservice.ChatEvent;
import io.smallrye.mutiny.Multi;

public sealed interface SubAgentTaskExecution
        permits SubAgentTaskExecution.Immediate, SubAgentTaskExecution.Streaming {

    static SubAgentTaskExecution immediate(String response) {
        return new Immediate(response);
    }

    static SubAgentTaskExecution streaming(Multi<ChatEvent> events) {
        return new Streaming(events);
    }

    record Immediate(String response) implements SubAgentTaskExecution {}

    record Streaming(Multi<ChatEvent> events) implements SubAgentTaskExecution {}
}
