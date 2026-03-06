package org.acme;

import jakarta.ws.rs.GET;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.Produces;
import jakarta.ws.rs.core.MediaType;
import java.util.Map;
import org.eclipse.microprofile.config.inject.ConfigProperty;

@Path("/config.json")
public class FrontendConfigResource {

    @ConfigProperty(name = "chat.frontend.keycloak-url", defaultValue = "http://localhost:8081")
    String keycloakUrl;

    @ConfigProperty(name = "chat.frontend.keycloak-realm", defaultValue = "memory-service")
    String keycloakRealm;

    @ConfigProperty(name = "chat.frontend.keycloak-client-id", defaultValue = "frontend")
    String keycloakClientId;

    @GET
    @Produces(MediaType.APPLICATION_JSON)
    public Map<String, String> getConfig() {
        return Map.of(
                "keycloakUrl", keycloakUrl,
                "keycloakRealm", keycloakRealm,
                "keycloakClientId", keycloakClientId);
    }
}
