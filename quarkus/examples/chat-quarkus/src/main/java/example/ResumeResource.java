package example;

import static io.github.chirino.memory.security.SecurityHelper.bearerToken;

import io.github.chirino.memory.history.runtime.ResponseResumer;
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

    @Inject SecurityIdentity securityIdentity;

    @POST
    @Produces(MediaType.APPLICATION_JSON)
    @Consumes(MediaType.APPLICATION_JSON)
    public List<String> check(List<String> conversationIds) {
        return responseResumer.check(conversationIds, bearerToken(securityIdentity));
    }
}
