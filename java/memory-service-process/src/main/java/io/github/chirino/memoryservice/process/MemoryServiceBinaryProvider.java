package io.github.chirino.memoryservice.process;

import java.io.IOException;
import java.io.InputStream;

/**
 * Optional service-provider interface for supplying a Memory Service executable to {@link
 * MemoryServiceBinary#packaged()}.
 *
 * <p>Release builds of {@code memory-service-process} already bundle the OCI descriptors for every
 * supported platform, so applications don't need a provider. A provider that matches the current
 * platform takes precedence over the bundled descriptor, for example a JAR that embeds an
 * executable for offline use.
 *
 * <p>Providers are discovered with {@link java.util.ServiceLoader}. Applications that create an
 * uber-JAR must merge {@code META-INF/services} resources.
 *
 * <p>A provider either embeds the executable and serves it from {@link #openBinary()}, or returns
 * an OCI descriptor from {@link #openOciDescriptor()} so the executable is downloaded from a
 * registry on first use. In both cases {@link #openSha256()} names the executable's digest.
 */
public interface MemoryServiceBinaryProvider {

    /** Platform served by this provider. */
    MemoryServicePlatform platform();

    /**
     * Opens a new stream containing the embedded native executable. Not called when {@link
     * #openOciDescriptor()} returns a stream.
     */
    default InputStream openBinary() throws IOException {
        throw new IOException("No embedded executable for " + platform().id());
    }

    /** Opens a new stream containing the executable's hexadecimal SHA-256 digest. */
    InputStream openSha256() throws IOException;

    /**
     * Opens a new stream containing the OCI descriptor ({@code memory-service.oci.properties}) for
     * a downloadable executable, or returns {@code null} when the executable is embedded.
     */
    default InputStream openOciDescriptor() throws IOException {
        return null;
    }
}
