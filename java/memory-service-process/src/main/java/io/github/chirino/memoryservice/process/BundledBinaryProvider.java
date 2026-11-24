package io.github.chirino.memoryservice.process;

import java.io.IOException;
import java.io.InputStream;
import java.util.Optional;

/**
 * The OCI descriptor and digest that release builds package into this JAR under {@code
 * META-INF/memory-service/<platform>/}. Used when no {@link MemoryServiceBinaryProvider} on the
 * classpath serves the platform.
 */
final class BundledBinaryProvider implements MemoryServiceBinaryProvider {

    static final String ROOT = "META-INF/memory-service/";
    static final String DESCRIPTOR = "memory-service.oci.properties";
    static final String SHA256 = "memory-service.sha256";

    private final MemoryServicePlatform platform;
    private final ClassLoader resources;

    private BundledBinaryProvider(MemoryServicePlatform platform, ClassLoader resources) {
        this.platform = platform;
        this.resources = resources;
    }

    /** The bundled descriptor for a platform, when this build packaged one. */
    static Optional<BundledBinaryProvider> find(
            MemoryServicePlatform platform, ClassLoader resources) {
        if (resources.getResource(ROOT + platform.id() + "/" + DESCRIPTOR) == null) {
            return Optional.empty();
        }
        return Optional.of(new BundledBinaryProvider(platform, resources));
    }

    @Override
    public MemoryServicePlatform platform() {
        return platform;
    }

    @Override
    public InputStream openSha256() throws IOException {
        return required(SHA256);
    }

    @Override
    public InputStream openOciDescriptor() throws IOException {
        return required(DESCRIPTOR);
    }

    private InputStream required(String name) throws IOException {
        String resource = ROOT + platform.id() + "/" + name;
        InputStream result = resources.getResourceAsStream(resource);
        if (result == null) {
            throw new IOException("Missing packaged resource " + resource);
        }
        return result;
    }

    @Override
    public String toString() {
        return "descriptor bundled in memory-service-process for " + platform.id();
    }
}
