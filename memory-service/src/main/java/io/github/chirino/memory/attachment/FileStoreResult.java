package io.github.chirino.memory.attachment;

/** Result of storing a file in a FileStore. */
public record FileStoreResult(String storageKey, long size) {}
