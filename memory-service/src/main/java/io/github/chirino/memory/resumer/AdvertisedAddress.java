package io.github.chirino.memory.resumer;

import java.util.Locale;
import java.util.Objects;
import java.util.Optional;

public final class AdvertisedAddress {
    private final String host;
    private final int port;

    public AdvertisedAddress(String host, int port) {
        this.host = host;
        this.port = port;
    }

    public String host() {
        return host;
    }

    public int port() {
        return port;
    }

    public boolean matches(AdvertisedAddress other) {
        if (other == null) {
            return false;
        }
        return port == other.port && host.equalsIgnoreCase(other.host);
    }

    public String authority() {
        if (host.contains(":") && !host.startsWith("[")) {
            return "[" + host + "]:" + port;
        }
        return host + ":" + port;
    }

    public static Optional<AdvertisedAddress> parse(String value) {
        if (value == null || value.isBlank()) {
            return Optional.empty();
        }
        String trimmed = value.trim();
        int portSeparator = findPortSeparator(trimmed);
        if (portSeparator <= 0 || portSeparator >= trimmed.length() - 1) {
            return Optional.empty();
        }
        String hostPart = trimmed.substring(0, portSeparator).trim();
        String portPart = trimmed.substring(portSeparator + 1).trim();
        return fromHostAndPort(hostPart, portPart);
    }

    public static Optional<AdvertisedAddress> fromHostAndPort(String host, String portValue) {
        if (host == null || host.isBlank()) {
            return Optional.empty();
        }
        String trimmedHost = normalizeHost(host.trim());
        if (trimmedHost.isEmpty()) {
            return Optional.empty();
        }
        int port = parsePort(portValue);
        if (port <= 0) {
            return Optional.empty();
        }
        return Optional.of(new AdvertisedAddress(trimmedHost, port));
    }

    public static Optional<AdvertisedAddress> fromAuthority(String authority) {
        if (authority == null || authority.isBlank()) {
            return Optional.empty();
        }
        String trimmed = authority.trim();
        int portSeparator = findPortSeparator(trimmed);
        if (portSeparator <= 0 || portSeparator >= trimmed.length() - 1) {
            return Optional.empty();
        }
        String hostPart = trimmed.substring(0, portSeparator).trim();
        String portPart = trimmed.substring(portSeparator + 1).trim();
        return fromHostAndPort(hostPart, portPart);
    }

    private static int parsePort(String portValue) {
        if (portValue == null || portValue.isBlank()) {
            return -1;
        }
        try {
            int port = Integer.parseInt(portValue.trim());
            return port > 0 ? port : -1;
        } catch (NumberFormatException e) {
            return -1;
        }
    }

    private static int findPortSeparator(String value) {
        if (value.startsWith("[")) {
            int closeBracket = value.indexOf(']');
            if (closeBracket >= 0
                    && closeBracket + 1 < value.length()
                    && value.charAt(closeBracket + 1) == ':') {
                return closeBracket + 1;
            }
        }
        return value.lastIndexOf(':');
    }

    private static String normalizeHost(String host) {
        String trimmed = host.trim();
        if (trimmed.startsWith("[") && trimmed.endsWith("]") && trimmed.length() > 2) {
            return trimmed.substring(1, trimmed.length() - 1);
        }
        return trimmed;
    }

    @Override
    public String toString() {
        return "AdvertisedAddress{host=" + host + ", port=" + port + "}";
    }

    @Override
    public boolean equals(Object obj) {
        if (this == obj) {
            return true;
        }
        if (!(obj instanceof AdvertisedAddress other)) {
            return false;
        }
        return port == other.port && host.equalsIgnoreCase(other.host);
    }

    @Override
    public int hashCode() {
        return Objects.hash(host.toLowerCase(Locale.ROOT), port);
    }
}
