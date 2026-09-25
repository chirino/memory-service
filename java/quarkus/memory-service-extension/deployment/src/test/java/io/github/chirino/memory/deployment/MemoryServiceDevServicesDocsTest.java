package io.github.chirino.memory.deployment;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.LinkedHashMap;
import java.util.Map;
import java.util.TreeSet;
import java.util.regex.Matcher;
import java.util.regex.Pattern;
import org.junit.jupiter.api.Test;
import org.testcontainers.containers.GenericContainer;
import org.testcontainers.utility.DockerImageName;

/**
 * Keeps the default container environment documented in the Quarkus Dev Services page in sync with
 * {@link MemoryServiceDevServicesProcessor}.
 */
class MemoryServiceDevServicesDocsTest {

    private static final Path DOC = Path.of("site/src/pages/docs/quarkus/dev-services.mdx");

    /** The generated agent API key is random per run and intentionally undocumented. */
    private static final String GENERATED_API_KEY = "MEMORY_SERVICE_API_KEYS_AGENT";

    private static final Pattern CODE_BLOCK = Pattern.compile("code=\\{`(.*?)`}", Pattern.DOTALL);
    private static final Pattern ENV_LINE = Pattern.compile("^(MEMORY_SERVICE_[A-Z0-9_]+)=(.*)$");
    private static final Pattern AUDIENCE_DEFAULT =
            Pattern.compile("defaults `MEMORY_SERVICE_OIDC_ALLOWED_AUDIENCES=([^`]+)`");

    @Test
    void documentedDefaultEnvironmentMatchesProcessor() throws IOException {
        Map<String, String> documented = documentedDefaultEnvironment(readDoc());
        for (String key :
                new String[] {
                    "MEMORY_SERVICE_DB_KIND",
                    "MEMORY_SERVICE_MANAGEMENT_ON_MAIN_LISTENER",
                    "MEMORY_SERVICE_HOST",
                    "MEMORY_SERVICE_PLAIN_TEXT",
                    "MEMORY_SERVICE_ENCRYPTION_KIND",
                    "MEMORY_SERVICE_ENCRYPTION_DEK_KEY"
                }) {
            assertTrue(documented.containsKey(key), "documented defaults are missing " + key);
        }

        GenericContainer<?> container =
                new GenericContainer<>(
                        DockerImageName.parse("example.invalid/memory-service:test"));
        MemoryServiceDevServicesProcessor.configureDefaultEnvironment(container, "test-api-key");
        Map<String, String> actual = new LinkedHashMap<>(container.getEnvMap());
        actual.remove(GENERATED_API_KEY);

        for (Map.Entry<String, String> entry : documented.entrySet()) {
            assertEquals(
                    entry.getValue(),
                    actual.get(entry.getKey()),
                    "documented default for " + entry.getKey() + " does not match the processor");
        }
        assertEquals(
                new TreeSet<>(actual.keySet()),
                new TreeSet<>(documented.keySet()),
                "processor default env keys and documented keys differ");
    }

    @Test
    void documentedOidcAudienceDefaultMatchesProcessor() throws IOException {
        Matcher matcher = AUDIENCE_DEFAULT.matcher(readDoc());
        assertTrue(matcher.find(), "doc no longer states the OIDC audience default");

        GenericContainer<?> container =
                new GenericContainer<>(
                        DockerImageName.parse("example.invalid/memory-service:test"));
        MemoryServiceDevServicesProcessor.configureKeycloakDevService(
                container, "http://localhost:8081/realms/memory-service");

        assertEquals(
                matcher.group(1),
                container.getEnvMap().get("MEMORY_SERVICE_OIDC_ALLOWED_AUDIENCES"));
    }

    /** Parses KEY=VALUE lines from the first code block that lists MEMORY_SERVICE_ variables. */
    private static Map<String, String> documentedDefaultEnvironment(String doc) {
        Matcher block = CODE_BLOCK.matcher(doc);
        while (block.find()) {
            Map<String, String> env = new LinkedHashMap<>();
            for (String line : block.group(1).split("\\R")) {
                Matcher envLine = ENV_LINE.matcher(line.strip());
                if (envLine.matches()) {
                    env.put(envLine.group(1), envLine.group(2).strip());
                }
            }
            if (!env.isEmpty()) {
                return env;
            }
        }
        throw new AssertionError("no MEMORY_SERVICE_ KEY=VALUE code block found in " + DOC);
    }

    private static String readDoc() throws IOException {
        Path doc = locateDoc();
        assertNotNull(doc, DOC + " not found above " + Path.of("").toAbsolutePath());
        return Files.readString(doc);
    }

    /** Walks up from the module directory to the repository root containing the site docs. */
    private static Path locateDoc() {
        for (Path dir = Path.of("").toAbsolutePath(); dir != null; dir = dir.getParent()) {
            Path candidate = dir.resolve(DOC);
            if (Files.isRegularFile(candidate)) {
                return candidate;
            }
        }
        return null;
    }
}
