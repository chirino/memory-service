package example;

import io.quarkus.security.identity.SecurityIdentity;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.ws.rs.GET;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.Produces;
import jakarta.ws.rs.core.MediaType;
import java.util.HashMap;
import java.util.Map;
import org.eclipse.microprofile.jwt.JsonWebToken;

/**
 * Returns information about the current authenticated user.
 * With bearer token authentication, user info comes from the access token JWT claims.
 */
@Path("/v1/me")
@ApplicationScoped
public class CurrentUserResource {

    @Inject SecurityIdentity securityIdentity;

    @GET
    @Produces(MediaType.APPLICATION_JSON)
    public Map<String, String> getCurrentUser() {
        String userId = null;
        String name = null;
        String email = null;

        // Get user info from access token JWT claims
        if (securityIdentity.getPrincipal() instanceof JsonWebToken jwt) {
            userId = jwt.getClaim("preferred_username");
            if (userId == null) {
                userId = jwt.getName();
            }
            name = jwt.getClaim("name");
            email = jwt.getClaim("email");
        }

        // Fall back to principal name for userId
        if (userId == null) {
            userId = securityIdentity.getPrincipal().getName();
        }

        Map<String, String> result = new HashMap<>();
        result.put("userId", userId != null ? userId : "anonymous");
        if (name != null) {
            result.put("name", name);
        }
        if (email != null) {
            result.put("email", email);
        }
        return result;
    }
}
