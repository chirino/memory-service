package io.github.chirino.memoryservice.spring.autoconfigure;

import static org.assertj.core.api.Assertions.assertThat;

import io.github.chirino.memoryservice.history.ConversationHistoryAutoConfiguration;
import io.github.chirino.memoryservice.memory.ChatMemoryAutoConfiguration;
import java.io.IOException;
import java.io.InputStream;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import java.util.Arrays;
import java.util.List;
import org.junit.jupiter.api.Test;
import org.springframework.boot.autoconfigure.AutoConfiguration;
import org.springframework.boot.context.annotation.ImportCandidates;

/**
 * Guards the copied {@code META-INF/spring} auto-configuration metadata. If it goes missing from
 * the built JAR (for example after a maven-compiler-plugin incremental rebuild deletes copied
 * resources), downstream Spring Boot apps silently skip Memory Service auto-configuration.
 */
class AutoConfigurationImportsTest {

    private static final String IMPORTS =
            "META-INF/spring/org.springframework.boot.autoconfigure.AutoConfiguration.imports";

    private static final List<String> EXPECTED =
            List.of(
                    MemoryServiceAutoConfiguration.class.getName(),
                    ConversationHistoryAutoConfiguration.class.getName(),
                    ChatMemoryAutoConfiguration.class.getName());

    @Test
    void importsResourceListsMemoryServiceAutoConfigurations() throws IOException {
        URL resource = MemoryServiceAutoConfiguration.class.getClassLoader().getResource(IMPORTS);
        assertThat(resource).as("classpath resource %s", IMPORTS).isNotNull();

        List<String> listed;
        try (InputStream in = resource.openStream()) {
            listed =
                    Arrays.stream(new String(in.readAllBytes(), StandardCharsets.UTF_8).split("\n"))
                            .map(String::strip)
                            .filter(line -> !line.isEmpty() && !line.startsWith("#"))
                            .toList();
        }
        assertThat(listed).containsAll(EXPECTED);
    }

    @Test
    void springBootDiscoversMemoryServiceAutoConfigurations() {
        List<String> candidates =
                ImportCandidates.load(
                                AutoConfiguration.class,
                                MemoryServiceAutoConfiguration.class.getClassLoader())
                        .getCandidates();
        assertThat(candidates).containsAll(EXPECTED);
    }
}
