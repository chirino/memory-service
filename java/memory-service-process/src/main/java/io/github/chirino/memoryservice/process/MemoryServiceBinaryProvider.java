package io.github.chirino.memoryservice.process;

import java.io.IOException;
import java.io.InputStream;

/**
 * Service-provider interface implemented by platform-specific Memory Service binary JARs.
 *
 * <p>Providers are discovered with {@link java.util.ServiceLoader}. Applications that create an
 * uber-JAR must merge {@code META-INF/services} resources.
 */
public interface MemoryServiceBinaryProvider {

    /** Platform served by this provider. */
    MemoryServicePlatform platform();

    /** Opens a new stream containing the native executable. */
    InputStream openBinary() throws IOException;

    /** Opens a new stream containing the executable's hexadecimal SHA-256 digest. */
    InputStream openSha256() throws IOException;
}
