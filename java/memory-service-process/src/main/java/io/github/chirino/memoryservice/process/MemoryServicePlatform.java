package io.github.chirino.memoryservice.process;

import java.util.Locale;
import java.util.Objects;

/** Operating-system and architecture identity used by packaged Memory Service binaries. */
public record MemoryServicePlatform(String operatingSystem, String architecture) {

    public MemoryServicePlatform {
        operatingSystem = requireToken(operatingSystem, "operatingSystem");
        architecture = requireToken(architecture, "architecture");
    }

    /** Detects and normalizes the current JVM platform. */
    public static MemoryServicePlatform current() {
        return from(System.getProperty("os.name", ""), System.getProperty("os.arch", ""));
    }

    /** Normalizes common JVM operating-system and architecture aliases. */
    public static MemoryServicePlatform from(String osName, String osArch) {
        String os = Objects.requireNonNull(osName, "osName").toLowerCase(Locale.ROOT);
        String arch = Objects.requireNonNull(osArch, "osArch").toLowerCase(Locale.ROOT);

        String normalizedOs;
        if (os.contains("linux")) {
            normalizedOs = "linux";
        } else if (os.contains("mac") || os.contains("darwin")) {
            normalizedOs = "macos";
        } else if (os.contains("windows")) {
            normalizedOs = "windows";
        } else {
            normalizedOs = os.replaceAll("[^a-z0-9]+", "-").replaceAll("(^-|-$)", "");
        }

        String normalizedArch =
                switch (arch) {
                    case "amd64", "x86_64", "x64" -> "amd64";
                    case "aarch64", "arm64" -> "arm64";
                    default -> arch.replaceAll("[^a-z0-9]+", "-").replaceAll("(^-|-$)", "");
                };
        return new MemoryServicePlatform(normalizedOs, normalizedArch);
    }

    /** Stable platform identifier used in artifact resources and extraction paths. */
    public String id() {
        return operatingSystem + "-" + architecture;
    }

    private static String requireToken(String value, String name) {
        String result = Objects.requireNonNull(value, name).trim().toLowerCase(Locale.ROOT);
        if (result.isEmpty()) {
            throw new IllegalArgumentException(name + " must not be blank");
        }
        return result;
    }
}
