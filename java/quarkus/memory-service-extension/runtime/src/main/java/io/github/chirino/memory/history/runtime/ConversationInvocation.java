package io.github.chirino.memory.history.runtime;

import java.util.List;

public record ConversationInvocation(
        String conversationId,
        String userMessage,
        List<AttachmentDescriptor> attachments,
        String agentId,
        String forkedAtConversationId,
        String forkedAtEntryId,
        String startedByConversationId,
        String startedByEntryId) {}
