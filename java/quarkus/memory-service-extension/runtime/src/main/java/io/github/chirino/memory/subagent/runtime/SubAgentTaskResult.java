package io.github.chirino.memory.subagent.runtime;

public record SubAgentTaskResult(
        String parentConversationId,
        String childConversationId,
        String childAgentId,
        SubAgentTaskStatus status,
        String lastMessage,
        String streamedResponseSoFar,
        String lastResponse,
        String lastError) {}
