package io.github.chirino.memory.subagent.runtime;

public record SubAgentTaskRequest(
        String parentConversationId,
        String childConversationId,
        String message,
        String childAgentId) {}
