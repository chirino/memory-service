package example;

import io.github.chirino.memory.conversation.runtime.ResponseResumer;
import io.quarkus.oidc.AccessTokenCredential;
import io.quarkus.security.credential.TokenCredential;
import io.quarkus.security.identity.SecurityIdentity;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.ws.rs.Consumes;
import jakarta.ws.rs.POST;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.Produces;
import jakarta.ws.rs.core.MediaType;
import java.util.List;
import org.jboss.logging.Logger;

/**
 * Implements the /v1/conversations/resume-check endpoint to check if conversations have responses in progress.
 */
@Path("/v1/conversations/resume-check")
@ApplicationScoped
public class ResumeResource {

    private static final Logger LOG = Logger.getLogger(ResumeResource.class);

    @Inject ResponseResumer responseResumer;

    @Inject SecurityIdentity identity;

    @POST
    @Produces(MediaType.APPLICATION_JSON)
    @Consumes(MediaType.APPLICATION_JSON)
    public List<String> check(List<String> conversationIds) {
        String principal =
                identity != null && identity.getPrincipal() != null
                        ? identity.getPrincipal().getName()
                        : "unknown";
        int count = conversationIds == null ? 0 : conversationIds.size();
        LOG.infof("Resume check requested by %s for %d conversation(s)", principal, count);
        String bearerToken = resolveBearerToken();
        if (bearerToken != null) {
            LOG.debug("Propagating bearer token to response resumer");
        }
        return responseResumer.check(conversationIds, bearerToken);
    }

    private String resolveBearerToken() {
        if (identity == null) {
            return null;
        }
        AccessTokenCredential atc = identity.getCredential(AccessTokenCredential.class);
        if (atc != null) {
            return atc.getToken();
        }
        TokenCredential tc = identity.getCredential(TokenCredential.class);
        if (tc != null) {
            return tc.getToken();
        }
        return null;
    }
}
