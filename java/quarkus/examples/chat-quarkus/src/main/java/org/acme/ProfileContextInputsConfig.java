package org.acme;

import io.smallrye.config.ConfigMapping;
import io.smallrye.config.WithDefault;

@ConfigMapping(prefix = "chat.profile-context.inputs")
public interface ProfileContextInputsConfig {

    @WithDefault("50")
    int maxItems();

    @WithDefault("1000")
    int maxItemChars();
}
