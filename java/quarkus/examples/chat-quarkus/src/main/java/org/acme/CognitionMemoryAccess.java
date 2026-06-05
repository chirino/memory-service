package org.acme;

import static io.github.chirino.memory.security.SecurityHelper.bearerToken;
import static io.github.chirino.memory.security.SecurityHelper.principalName;

import io.github.chirino.memory.client.api.MemoriesApi;
import io.github.chirino.memory.runtime.MemoryServiceApiBuilder;
import io.quarkus.security.identity.SecurityIdentity;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.enterprise.inject.Instance;
import jakarta.inject.Inject;
import jakarta.ws.rs.NotAuthorizedException;
import java.util.ArrayList;
import java.util.List;

@ApplicationScoped
public class CognitionMemoryAccess {

    @Inject Instance<SecurityIdentity> identity;
    @Inject MemoryServiceApiBuilder apiBuilder;
    @Inject CognitionMemoryRagConfig ragConfig;

    public String userId() {
        String userId = principalName(identity);
        if (userId == null || userId.isBlank()) {
            throw new NotAuthorizedException("No active request identity");
        }
        return userId;
    }

    public MemoriesApi memoriesApi() {
        String token = bearerToken(identity);
        if (token == null || token.isBlank()) {
            throw new NotAuthorizedException("No bearer token");
        }
        return apiBuilder.withBearerAuth(token).build(MemoriesApi.class);
    }

    public List<String> cognitionPrefix(String userId) {
        return List.of("user", userId, ragConfig.namespaceRoot());
    }

    public List<String> cognitionNamespace(String userId, String finalSegment) {
        List<String> namespace = new ArrayList<>(cognitionPrefix(userId));
        namespace.add(finalSegment);
        return List.copyOf(namespace);
    }

    public List<String> profileContextNamespace(String userId) {
        return cognitionNamespace(userId, "profile_context");
    }

    public List<String> profileInputNamespace(String userId) {
        return cognitionNamespace(userId, "profile_input");
    }
}
