package example;

import io.quarkus.security.identity.SecurityIdentity;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.ws.rs.GET;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.Produces;
import jakarta.ws.rs.core.MediaType;
import java.util.Map;
import org.eclipse.microprofile.jwt.JsonWebToken;

/**
 * Returns information about the current authenticated user.
 */
@Path("/v1/me")
@ApplicationScoped
public class CurrentUserResource {

    @Inject SecurityIdentity securityIdentity;

    @GET
    @Produces(MediaType.APPLICATION_JSON)
    public Map<String, String> getCurrentUser() {
        String userId = null;

        // Try to get preferred_username from JWT claims
        if (securityIdentity.getPrincipal() instanceof JsonWebToken jwt) {
            userId = jwt.getClaim("preferred_username");
            if (userId == null) {
                userId = jwt.getName();
            }
        }

        // Fall back to principal name
        if (userId == null) {
            userId = securityIdentity.getPrincipal().getName();
        }

        return Map.of("userId", userId != null ? userId : "anonymous");
    }
}
