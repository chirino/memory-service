package io.github.chirino.memory.subagent.runtime;

import java.time.Instant;

public record SubAgentTaskResult(
        String parentConversationId,
        String childConversationId,
        String childAgentId,
        SubAgentTaskStatus status,
        String lastMessage,
        String streamedResponseSoFar,
        String lastResponse,
        String lastError,
        String queuedMessage,
        Instant queuedAt,
        long runId,
        Instant startedAt,
        Instant updatedAt) {}
