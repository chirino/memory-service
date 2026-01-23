package io.github.chirino.memoryservice.spring.autoconfigure.serviceconnection;

import java.net.URI;
import org.springframework.boot.autoconfigure.service.connection.ConnectionDetails;

/**
 * Connection details for a running memory-service instance.
 */
public interface MemoryServiceConnectionDetails extends ConnectionDetails {

    /**
     * Base URI that clients should use to reach the memory-service REST API.
     */
    URI getBaseUri();

    /**
     * Convenience accessor returning the base URI as a string.
     */
    default String getBaseUrl() {
        return getBaseUri().toString();
    }
}
