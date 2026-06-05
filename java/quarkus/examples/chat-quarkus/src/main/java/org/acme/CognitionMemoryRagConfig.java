package org.acme;

import io.smallrye.config.ConfigMapping;
import io.smallrye.config.WithDefault;

@ConfigMapping(prefix = "chat.cognition-rag")
public interface CognitionMemoryRagConfig {

    @WithDefault("false")
    boolean enabled();

    @WithDefault("cognition.v1")
    String namespaceRoot();

    ProfileContext profileContext();

    AdditionalSearch additionalSearch();

    @WithDefault("8")
    int limit();

    @WithDefault("24")
    int minQueryChars();

    @WithDefault("0.82")
    double minSemanticScore();

    @WithDefault("4000")
    int maxChars();

    @WithDefault("true")
    boolean includeUsage();

    @WithDefault("true")
    boolean failOpen();

    interface ProfileContext {
        @WithDefault("true")
        boolean enabled();

        @WithDefault("latest")
        String key();
    }

    interface AdditionalSearch {
        @WithDefault("true")
        boolean enabled();
    }
}
