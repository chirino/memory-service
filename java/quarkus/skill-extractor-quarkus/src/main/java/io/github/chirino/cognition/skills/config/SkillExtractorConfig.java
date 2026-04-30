package io.github.chirino.cognition.skills.config;

import io.smallrye.config.ConfigMapping;
import io.smallrye.config.WithDefault;

@ConfigMapping(prefix = "skill")
public interface SkillExtractorConfig {

    MemoryServiceConfig memoryService();

    ExtractionConfig extraction();

    interface MemoryServiceConfig {
        String baseUrl();

        String apiKey();

        @WithDefault("cognition-processor")
        String clientId();
    }

    interface ExtractionConfig {
        @WithDefault("true")
        boolean enabled();

        @WithDefault("5")
        int maxEntriesPerCluster();

        @WithDefault("5")
        int minClusterMembers();

        @WithDefault("10")
        int maxSkillsPerCluster();
    }
}
