package io.github.chirino.memory.store;

import java.util.UUID;

/**
 * Identifies a unique epoch for a specific agent in a conversation.
 *
 * <p>Epochs are scoped per-client: each agent (clientId) has its own epoch sequence per
 * conversation. This record represents the tuple (conversationId, clientId, epoch) that uniquely
 * identifies a memory epoch.
 */
public record EpochKey(UUID conversationId, String clientId, long epoch) {}
