package io.github.chirino.memory.store;

public class ResourceConflictException extends RuntimeException {

    private final String resource;
    private final String id;

    public ResourceConflictException(String resource, String id, String message) {
        super(message);
        this.resource = resource;
        this.id = id;
    }

    public String getResource() {
        return resource;
    }

    public String getId() {
        return id;
    }
}
