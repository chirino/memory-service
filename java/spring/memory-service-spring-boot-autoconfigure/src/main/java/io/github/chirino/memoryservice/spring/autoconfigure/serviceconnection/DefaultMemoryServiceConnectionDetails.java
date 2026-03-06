package io.github.chirino.memoryservice.spring.autoconfigure.serviceconnection;

import java.net.URI;
import java.util.Objects;

/**
 * Simple immutable connection details implementation.
 */
public record DefaultMemoryServiceConnectionDetails(URI baseUri, String apiKey)
        implements MemoryServiceConnectionDetails {

    public DefaultMemoryServiceConnectionDetails {
        Objects.requireNonNull(baseUri, "baseUri must not be null");
    }

    @Override
    public URI getBaseUri() {
        return baseUri;
    }

    @Override
    public String getApiKey() {
        return apiKey;
    }
}
