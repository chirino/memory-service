package io.github.chirino.memory.security;

import io.quarkus.oidc.AccessTokenCredential;
import io.quarkus.security.credential.TokenCredential;
import io.quarkus.security.identity.SecurityIdentity;
import jakarta.enterprise.inject.Instance;

public final class SecurityHelper {

    private SecurityHelper() {}

    public static String bearerToken(Instance<SecurityIdentity> identityInstance) {
        return bearerToken(resolveIdentity(identityInstance));
    }

    public static String bearerToken(SecurityIdentity identity) {
        if (identity == null) {
            return null;
        }
        AccessTokenCredential accessToken = identity.getCredential(AccessTokenCredential.class);
        if (accessToken != null) {
            return accessToken.getToken();
        }
        TokenCredential tokenCredential = identity.getCredential(TokenCredential.class);
        if (tokenCredential != null) {
            return tokenCredential.getToken();
        }
        return null;
    }

    public static String principalName(Instance<SecurityIdentity> identityInstance) {
        return principalName(resolveIdentity(identityInstance));
    }

    public static String principalName(SecurityIdentity identity) {
        if (identity == null || identity.getPrincipal() == null) {
            return null;
        }
        return identity.getPrincipal().getName();
    }

    private static SecurityIdentity resolveIdentity(Instance<SecurityIdentity> identityInstance) {
        return identityInstance != null && identityInstance.isResolvable()
                ? identityInstance.get()
                : null;
    }
}
