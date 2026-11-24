package io.github.chirino.memoryservice.process;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import io.github.chirino.memoryservice.process.MemoryServiceBinaryRegistries.Credentials;
import io.github.chirino.memoryservice.process.MemoryServiceBinaryRegistries.Location;
import java.nio.file.Path;
import java.time.Duration;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import org.junit.jupiter.api.Test;

class MemoryServiceBinaryRegistriesTest {

    private static final String UPSTREAM = "ghcr.io/chirino/memory-service-binary";

    @Test
    void defaultsUseOnlyTheUpstreamRegistry() {
        List<Location> locations = MemoryServiceBinaryRegistries.defaults().locations(UPSTREAM);

        assertEquals(
                List.of(new Location("https", "ghcr.io", "chirino/memory-service-binary", false)),
                locations);
    }

    @Test
    void mirrorRulesAppendTheRestOfTheRepository() {
        assertEquals(
                "harbor.corp.example/ghcr-proxy/chirino/memory-service-binary",
                only("ghcr.io", "harbor.corp.example/ghcr-proxy").toString());
        assertEquals(
                "nexus.corp.example:8443/tools/memory-service-binary",
                only(UPSTREAM, "nexus.corp.example:8443/tools/memory-service-binary").toString());
        Location plain = only("ghcr.io", "http://zot.lab.internal:5000");
        assertEquals("http", plain.scheme());
        assertEquals("zot.lab.internal:5000", plain.authority());
        assertEquals("chirino/memory-service-binary", plain.name());
        assertEquals("https", only("ghcr.io", "https://mirror.example").scheme());
    }

    @Test
    void longestPrefixWinsAndSamePrefixRulesKeepTheirOrder() {
        MemoryServiceBinaryRegistries registries =
                MemoryServiceBinaryRegistries.builder()
                        .mirror("ghcr.io", "short.example")
                        .mirror("ghcr.io/chirino", "first.example/c")
                        .mirror("ghcr.io/chirino", "second.example/c")
                        .mirror("ghcr.io/chirinox", "never.example/x")
                        .build();

        List<Location> locations = registries.locations(UPSTREAM);

        assertEquals(2, locations.size());
        assertEquals("first.example/c/memory-service-binary", locations.get(0).toString());
        assertEquals("second.example/c/memory-service-binary", locations.get(1).toString());
        assertTrue(locations.get(0).mirror());
    }

    @Test
    void upstreamFallbackIsOffOnceMirrorsMatchUnlessEnabled() {
        assertEquals(
                1,
                MemoryServiceBinaryRegistries.builder()
                        .mirror("ghcr.io", "mirror.example")
                        .build()
                        .locations(UPSTREAM)
                        .size());

        List<Location> withFallback =
                MemoryServiceBinaryRegistries.builder()
                        .mirror("ghcr.io", "mirror.example")
                        .upstreamFallback(true)
                        .build()
                        .locations(UPSTREAM);
        assertEquals(2, withFallback.size());
        assertEquals("ghcr.io/chirino/memory-service-binary", withFallback.get(1).toString());
        assertFalse(withFallback.get(1).mirror());

        List<Location> unmatched =
                MemoryServiceBinaryRegistries.builder()
                        .mirror("docker.io", "mirror.example")
                        .build()
                        .locations(UPSTREAM);
        assertEquals(List.of(Location.upstream(UPSTREAM)), unmatched);
    }

    @Test
    void rejectsMalformedMirrorRules() {
        assertInvalid(() -> mirror("https://ghcr.io", "mirror.example"), "scheme");
        assertInvalid(() -> mirror("", "mirror.example"), "blank");
        assertInvalid(() -> mirror("ghcr.io/", "mirror.example"), "repository path");
        assertInvalid(() -> mirror("ghcr.io", "ftp://mirror.example"), "http:// or https://");
        assertInvalid(() -> mirror("ghcr.io", "mirror example"), "registry host");
        assertInvalid(() -> mirror(UPSTREAM, "http://zot:5000"), "needs a repository path");
    }

    @Test
    void validatesCredentialsAndCertificates() {
        assertInvalid(
                () -> MemoryServiceBinaryRegistries.builder().credentials(" ", "u", "s"), "blank");
        assertInvalid(
                () ->
                        MemoryServiceBinaryRegistries.builder()
                                .credentials("https://ghcr.io", "u", "s"),
                "without a scheme");
        assertInvalid(
                () ->
                        MemoryServiceBinaryRegistries.builder()
                                .mirror("ghcr.io", "http://zot.lab:5000")
                                .credentials("zot.lab:5000", "u", "s")
                                .build(),
                "http://");
        assertInvalid(
                () ->
                        MemoryServiceBinaryRegistries.builder()
                                .trustedCertificates(Path.of("does-not-exist.pem"))
                                .build(),
                "not found");
        assertInvalid(
                () -> MemoryServiceBinaryRegistries.builder().downloadTimeout(Duration.ZERO),
                "positive");
    }

    @Test
    void credentialSelectionPrefersExplicitThenGithubTokenThenAnonymous() {
        Map<String, String> environment = Map.of("GH_TOKEN", "gh", "GITHUB_TOKEN", "github");
        Location ghcr = Location.upstream(UPSTREAM);

        MemoryServiceBinaryRegistries explicit =
                MemoryServiceBinaryRegistries.builder()
                        .environment(environment)
                        .credentials("ghcr.io", "token", "configured")
                        .build();
        Credentials chosen = explicit.credentialsFor(ghcr).orElseThrow();
        assertEquals("configured", chosen.secret());
        assertFalse(chosen.implicit());
        assertEquals("credentials(ghcr.io)", chosen.toString());

        MemoryServiceBinaryRegistries implicit =
                MemoryServiceBinaryRegistries.builder().environment(environment).build();
        Credentials token = implicit.credentialsFor(ghcr).orElseThrow();
        assertEquals("gh", token.secret());
        assertEquals("GH_TOKEN", token.toString());
        assertTrue(token.implicit());

        assertEquals(
                "github",
                MemoryServiceBinaryRegistries.builder()
                        .environment(Map.of("GITHUB_TOKEN", "github"))
                        .build()
                        .credentialsFor(ghcr)
                        .orElseThrow()
                        .secret());

        Location mirror = new Location("https", "mirror.example", "x/y", true);
        assertEquals(Optional.empty(), implicit.credentialsFor(mirror));
        assertEquals(
                Optional.empty(),
                MemoryServiceBinaryRegistries.builder()
                        .environment(environment)
                        .githubTokenFromEnvironment(false)
                        .build()
                        .credentialsFor(ghcr));
    }

    @Test
    void plainHttpLocationsNeverGetCredentials() {
        MemoryServiceBinaryRegistries registries =
                MemoryServiceBinaryRegistries.builder()
                        .mirror("ghcr.io", "https://zot.lab:5000")
                        .credentials("zot.lab:5000", "u", "s")
                        .build();

        assertTrue(
                registries
                        .credentialsFor(new Location("https", "zot.lab:5000", "a/b", true))
                        .isPresent());
        assertEquals(
                Optional.empty(),
                registries.credentialsFor(new Location("http", "zot.lab:5000", "a/b", true)));
    }

    @Test
    void toStringNeverIncludesSecrets() {
        String text =
                MemoryServiceBinaryRegistries.builder()
                        .credentials("ghcr.io", "token", "super-secret")
                        .build()
                        .toString();

        assertTrue(text.contains("ghcr.io"));
        assertFalse(text.contains("super-secret"));
    }

    private static Location only(String prefix, String location) {
        List<Location> locations = mirror(prefix, location).build().locations(UPSTREAM);
        assertEquals(1, locations.size());
        return locations.get(0);
    }

    private static MemoryServiceBinaryRegistries.Builder mirror(String prefix, String location) {
        return MemoryServiceBinaryRegistries.builder().mirror(prefix, location);
    }

    private static void assertInvalid(Runnable action, String message) {
        IllegalArgumentException error = assertThrows(IllegalArgumentException.class, action::run);
        assertTrue(error.getMessage().contains(message), error.getMessage());
    }
}
