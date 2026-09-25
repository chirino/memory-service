package io.github.chirino.memoryservice.process.binaries;

/**
 * Marker type for the aggregate Memory Service native binary dependency.
 *
 * <p>Depending on this artifact makes every supported native binary provider available through
 * the {@code memory-service-process} API.
 *
 * <p>Keep this public type even though the JAR has no behavior: a {@code package-info.java} alone
 * is not enough for JDK 21 Javadoc generation under the inherited {@code central-release} profile.
 */
public final class MemoryServiceBinaries {
    private MemoryServiceBinaries() {}
}
