package org.acme;

import java.util.Map;
import org.springframework.beans.factory.annotation.Value;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.RestController;

@RestController
public class FrontendConfigController {

    @Value("${chat.frontend.keycloak-url:http://localhost:8081}")
    private String keycloakUrl;

    @Value("${chat.frontend.keycloak-realm:memory-service}")
    private String keycloakRealm;

    @Value("${chat.frontend.keycloak-client-id:frontend}")
    private String keycloakClientId;

    @GetMapping("/config.json")
    public Map<String, String> getConfig() {
        return Map.of(
                "keycloakUrl", keycloakUrl,
                "keycloakRealm", keycloakRealm,
                "keycloakClientId", keycloakClientId);
    }
}
