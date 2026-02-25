package io.github.chirino.memory.config;

import io.github.chirino.memory.security.AuthorizationService;
import io.github.chirino.memory.security.LocalAuthorizationService;
import io.github.chirino.memory.security.SpiceDbAuthorizationService;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.enterprise.inject.Instance;
import jakarta.inject.Inject;
import org.eclipse.microprofile.config.inject.ConfigProperty;

@ApplicationScoped
public class AuthorizationServiceSelector {

    @ConfigProperty(name = "memory-service.authz.type", defaultValue = "local")
    String authzType;

    @Inject Instance<LocalAuthorizationService> localAuthzService;

    @Inject Instance<SpiceDbAuthorizationService> spiceDbAuthzService;

    public AuthorizationService getAuthorizationService() {
        String type = authzType == null ? "local" : authzType.trim().toLowerCase();
        return switch (type) {
            case "spicedb" -> spiceDbAuthzService.get();
            default -> localAuthzService.get();
        };
    }
}
