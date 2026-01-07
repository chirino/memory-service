package io.github.chirino.memory.conversation.api;

import io.github.chirino.memory.conversation.model.ConversationMessage;

public record ConversationEvent(String conversationId, ConversationMessage message) {}
