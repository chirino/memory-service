package io.github.chirino.memoryservice.process;

import java.io.IOException;
import java.io.InputStream;

public final class TestBinaryProvider implements MemoryServiceBinaryProvider {

    @Override
    public MemoryServicePlatform platform() {
        return new MemoryServicePlatform("test", "test");
    }

    @Override
    public InputStream openBinary() throws IOException {
        return required("/test-memory-service");
    }

    @Override
    public InputStream openSha256() throws IOException {
        return required("/test-memory-service.sha256");
    }

    private InputStream required(String name) throws IOException {
        InputStream result = getClass().getResourceAsStream(name);
        if (result == null) {
            throw new IOException("missing test resource " + name);
        }
        return result;
    }
}
