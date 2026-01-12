package io.github.chirino.memory.history.api;

import io.github.chirino.memory.history.model.ConversationMessage;

public record ConversationEvent(String conversationId, ConversationMessage message) {}
