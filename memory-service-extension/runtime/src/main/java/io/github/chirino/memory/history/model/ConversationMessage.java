package io.github.chirino.memory.history.model;

public record ConversationMessage(MessageRole role, String content, long timestamp) {}
