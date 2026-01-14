package io.github.chirino.memory.resumer;

import java.util.Optional;

public final class ResponseResumerLocator {
    private final String host;
    private final int port;
    private final String fileName;

    public ResponseResumerLocator(String host, int port, String fileName) {
        this.host = host;
        this.port = port;
        this.fileName = fileName;
    }

    public String host() {
        return host;
    }

    public int port() {
        return port;
    }

    public String fileName() {
        return fileName;
    }

    public AdvertisedAddress address() {
        return new AdvertisedAddress(host, port);
    }

    public boolean matches(AdvertisedAddress advertisedAddress) {
        return advertisedAddress != null
                && host.equalsIgnoreCase(advertisedAddress.host())
                && port == advertisedAddress.port();
    }

    public String encode() {
        return host + "|" + port + "|" + fileName;
    }

    public static Optional<ResponseResumerLocator> decode(String value) {
        if (value == null || value.isBlank()) {
            return Optional.empty();
        }
        String[] parts = value.split("\\|", 3);
        if (parts.length < 3) {
            return Optional.empty();
        }
        int port = 0;
        try {
            port = Integer.parseInt(parts[1]);
        } catch (NumberFormatException e) {
            port = 0;
        }
        return Optional.of(new ResponseResumerLocator(parts[0], port, parts[2]));
    }
}
