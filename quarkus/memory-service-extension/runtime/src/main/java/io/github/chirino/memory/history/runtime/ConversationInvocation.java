package io.github.chirino.memory.history.runtime;

import java.util.List;
import java.util.Map;

public record ConversationInvocation(
        String conversationId, String userMessage, List<Map<String, Object>> attachments) {}
