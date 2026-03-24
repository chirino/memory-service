package io.github.chirino.memory.subagent.runtime;

import java.util.List;

public record SubAgentWaitResult(
        String parentConversationId, boolean allCompleted, List<SubAgentTaskResult> tasks) {}
