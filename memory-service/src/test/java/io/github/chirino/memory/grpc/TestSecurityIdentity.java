package io.github.chirino.memory.grpc;

import io.quarkus.security.credential.Credential;
import io.quarkus.security.identity.SecurityIdentity;
import io.smallrye.mutiny.Uni;
import java.security.Permission;
import java.security.Principal;
import java.util.Collections;
import java.util.Map;
import java.util.Set;

public final class TestSecurityIdentity {

    private TestSecurityIdentity() {}

    public static SecurityIdentity create(String name) {
        return new SecurityIdentity() {
            @Override
            public Principal getPrincipal() {
                return () -> name;
            }

            @Override
            public boolean isAnonymous() {
                return false;
            }

            @Override
            public Set<String> getRoles() {
                return Collections.emptySet();
            }

            @Override
            public boolean hasRole(String role) {
                return false;
            }

            @Override
            public <T extends Credential> T getCredential(Class<T> credentialType) {
                return null;
            }

            @Override
            public Set<Credential> getCredentials() {
                return Collections.emptySet();
            }

            @Override
            public <T> T getAttribute(String name) {
                return null;
            }

            @Override
            public Map<String, Object> getAttributes() {
                return Collections.emptyMap();
            }

            @Override
            public Uni<Boolean> checkPermission(Permission permission) {
                return Uni.createFrom().item(true);
            }
        };
    }
}
