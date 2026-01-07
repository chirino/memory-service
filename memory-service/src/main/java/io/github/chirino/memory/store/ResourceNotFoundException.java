package io.github.chirino.memory.store;

public class ResourceNotFoundException extends RuntimeException {

    private final String resource;
    private final String id;

    public ResourceNotFoundException(String resource, String id) {
        super(resource + " not found: " + id);
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
