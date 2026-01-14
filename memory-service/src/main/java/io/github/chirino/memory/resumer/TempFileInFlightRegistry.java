package io.github.chirino.memory.resumer;

import java.nio.file.Path;
import java.util.Map;
import java.util.concurrent.ConcurrentHashMap;

public final class TempFileInFlightRegistry {
    private final Map<String, TempFileRegistryEntry> entries = new ConcurrentHashMap<>();

    TempFileRegistryEntry register(String conversationId, Path filePath) {
        if (filePath == null) {
            return null;
        }
        TempFileRegistryEntry entry = new TempFileRegistryEntry(filePath);
        TempFileRegistryEntry previous = entries.put(conversationId, entry);
        if (previous != null) {
            previous.markClosed();
        }
        return entry;
    }

    TempFileRegistryEntry get(String conversationId) {
        return entries.get(conversationId);
    }

    void cleanupIfPossible(String conversationId, TempFileRegistryEntry entry) {
        if (entry == null) {
            return;
        }
        if (!entry.isClosable()) {
            return;
        }
        if (entry.tryDelete()) {
            entries.remove(conversationId, entry);
        }
    }

    void cleanupClosedEntries() {
        for (Map.Entry<String, TempFileRegistryEntry> entry : entries.entrySet()) {
            TempFileRegistryEntry value = entry.getValue();
            if (!value.isClosable()) {
                continue;
            }
            if (value.tryDelete()) {
                entries.remove(entry.getKey(), value);
            }
        }
    }
}
