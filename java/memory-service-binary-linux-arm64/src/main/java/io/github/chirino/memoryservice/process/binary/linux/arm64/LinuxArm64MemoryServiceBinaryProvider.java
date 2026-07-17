package io.github.chirino.memoryservice.process.binary.linux.arm64;

import io.github.chirino.memoryservice.process.MemoryServiceBinaryProvider;
import io.github.chirino.memoryservice.process.MemoryServicePlatform;
import java.io.IOException;
import java.io.InputStream;

/** Packaged Memory Service executable for Linux ARM64 JVMs. */
public final class LinuxArm64MemoryServiceBinaryProvider implements MemoryServiceBinaryProvider {

    private static final String ROOT = "/META-INF/memory-service/linux-arm64/";

    @Override
    public MemoryServicePlatform platform() {
        return new MemoryServicePlatform("linux", "arm64");
    }

    @Override
    public InputStream openBinary() throws IOException {
        return required(ROOT + "memory-service");
    }

    @Override
    public InputStream openSha256() throws IOException {
        return required(ROOT + "memory-service.sha256");
    }

    private InputStream required(String resource) throws IOException {
        InputStream result = getClass().getResourceAsStream(resource);
        if (result == null) {
            throw new IOException("Missing packaged resource " + resource);
        }
        return result;
    }
}
