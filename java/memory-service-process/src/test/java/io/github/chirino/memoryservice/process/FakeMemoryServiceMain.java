package io.github.chirino.memoryservice.process;

import java.io.ByteArrayOutputStream;
import java.net.InetSocketAddress;
import java.net.StandardProtocolFamily;
import java.net.UnixDomainSocketAddress;
import java.nio.ByteBuffer;
import java.nio.channels.ServerSocketChannel;
import java.nio.channels.SocketChannel;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.List;
import java.util.Map;

public final class FakeMemoryServiceMain {

    private FakeMemoryServiceMain() {}

    public static void main(String[] args) throws Exception {
        System.out.println("fake-memory-service-stdout");
        System.err.println("fake-memory-service-stderr");
        System.out.println("fake-memory-service-args=" + Arrays.toString(args));
        System.out.println(
                "fake-memory-service-db-kind=" + System.getenv("MEMORY_SERVICE_DB_KIND"));
        System.out.println(
                "fake-memory-service-embedding-kind="
                        + System.getenv("MEMORY_SERVICE_EMBEDDING_KIND"));
        String mode = System.getenv().getOrDefault("FAKE_MEMORY_SERVICE_MODE", "ready");
        if ("early-exit".equals(mode)) {
            System.exit(23);
        }
        if ("flood-output".equals(mode)) {
            for (int i = 0; i < 20_000; i++) {
                System.out.println("flood-stdout-" + i);
                System.err.println("flood-stderr-" + i);
            }
        }

        Map<String, String> environment = System.getenv();
        List<Path> socketPaths = new ArrayList<>();
        List<ServerSocketChannel> servers = new ArrayList<>();
        boolean managementOnMain =
                Boolean.parseBoolean(
                        environment.getOrDefault(
                                "MEMORY_SERVICE_MANAGEMENT_ON_MAIN_LISTENER", "false"));
        try {
            requireExclusive(environment, "MEMORY_SERVICE_UNIX_SOCKET", "MEMORY_SERVICE_PORT");
            ServerSocketChannel main =
                    openListener(
                            environment.get("MEMORY_SERVICE_UNIX_SOCKET"),
                            environment.getOrDefault("MEMORY_SERVICE_HOST", "127.0.0.1"),
                            intValue(environment.get("MEMORY_SERVICE_PORT"), 8080),
                            socketPaths);
            servers.add(main);

            ServerSocketChannel management = null;
            if (!managementOnMain) {
                String managementSocket = environment.get("MEMORY_SERVICE_MANAGEMENT_UNIX_SOCKET");
                String managementPort = environment.get("MEMORY_SERVICE_MANAGEMENT_PORT");
                if ((managementSocket != null && !managementSocket.isBlank())
                        || (managementPort != null && !managementPort.isBlank())) {
                    requireExclusive(
                            environment,
                            "MEMORY_SERVICE_MANAGEMENT_UNIX_SOCKET",
                            "MEMORY_SERVICE_MANAGEMENT_PORT");
                    management =
                            openListener(
                                    managementSocket,
                                    environment.getOrDefault(
                                            "MEMORY_SERVICE_MANAGEMENT_HOST", "127.0.0.1"),
                                    intValue(managementPort, 0),
                                    socketPaths);
                    servers.add(management);
                }
            }

            if (management != null) {
                ServerSocketChannel managementServer = management;
                Thread.ofPlatform()
                        .daemon(true)
                        .name("fake-memory-service-management")
                        .start(() -> serve(managementServer, true, mode));
            }
            serve(main, managementOnMain, mode);
        } finally {
            for (ServerSocketChannel server : servers) {
                server.close();
            }
            for (Path socketPath : socketPaths) {
                Files.deleteIfExists(socketPath);
            }
        }
    }

    private static void requireExclusive(
            Map<String, String> environment, String first, String second) {
        String firstValue = environment.get(first);
        String secondValue = environment.get(second);
        if (firstValue != null
                && !firstValue.isBlank()
                && secondValue != null
                && !secondValue.isBlank()) {
            throw new IllegalArgumentException(
                    first + " and " + second + " are mutually exclusive");
        }
    }

    private static ServerSocketChannel openListener(
            String unixSocket, String host, int port, List<Path> socketPaths) throws Exception {
        if (unixSocket != null && !unixSocket.isBlank()) {
            Path socketPath = Path.of(unixSocket);
            socketPaths.add(socketPath);
            Files.deleteIfExists(socketPath);
            ServerSocketChannel server = ServerSocketChannel.open(StandardProtocolFamily.UNIX);
            server.bind(UnixDomainSocketAddress.of(socketPath));
            return server;
        }
        ServerSocketChannel server = ServerSocketChannel.open();
        server.bind(new InetSocketAddress(configuredHost(host), port));
        return server;
    }

    private static String configuredHost(String value) {
        return value == null || value.isBlank() ? "127.0.0.1" : value.trim();
    }

    private static void serve(ServerSocketChannel server, boolean exposesReady, String mode) {
        try {
            while (true) {
                try (SocketChannel client = server.accept()) {
                    readHeaders(client);
                    boolean ready = exposesReady && !"never-ready".equals(mode);
                    byte[] body =
                            (ready ? "{\"status\":\"ready\"}" : "{\"status\":\"starting\"}")
                                    .getBytes(StandardCharsets.UTF_8);
                    String status = ready ? "200 OK" : "503 Service Unavailable";
                    ByteBuffer response =
                            ByteBuffer.wrap(
                                    ("HTTP/1.1 "
                                                    + status
                                                    + "\r\n"
                                                    + "Content-Type: application/json\r\n"
                                                    + "Content-Length: "
                                                    + body.length
                                                    + "\r\nConnection: close\r\n\r\n")
                                            .getBytes(StandardCharsets.US_ASCII));
                    while (response.hasRemaining()) {
                        client.write(response);
                    }
                    client.write(ByteBuffer.wrap(body));
                }
            }
        } catch (Exception e) {
            throw new RuntimeException(e);
        }
    }

    private static void readHeaders(SocketChannel client) throws Exception {
        ByteArrayOutputStream request = new ByteArrayOutputStream();
        ByteBuffer buffer = ByteBuffer.allocate(512);
        while (request.size() < 8192) {
            int count = client.read(buffer);
            if (count < 0) {
                return;
            }
            buffer.flip();
            request.write(buffer.array(), 0, buffer.remaining());
            buffer.clear();
            if (request.toString(StandardCharsets.US_ASCII).contains("\r\n\r\n")) {
                return;
            }
        }
    }

    private static int intValue(String value, int defaultValue) {
        return value == null || value.isBlank() ? defaultValue : Integer.parseInt(value);
    }
}
