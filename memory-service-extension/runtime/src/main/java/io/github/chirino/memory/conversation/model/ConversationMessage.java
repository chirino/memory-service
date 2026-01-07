package io.github.chirino.memory.conversation.model;

public record ConversationMessage(MessageRole role, String content, long timestamp) {}
