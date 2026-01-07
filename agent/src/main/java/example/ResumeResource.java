package example;

import io.github.chirino.memory.conversation.runtime.ResponseResumer;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.ws.rs.Consumes;
import jakarta.ws.rs.POST;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.Produces;
import jakarta.ws.rs.core.MediaType;
import java.util.List;

/**
 * Implements the /v1/conversations/resume-check endpoint to check if conversations have responses in progress.
 */
@Path("/v1/conversations/resume-check")
@ApplicationScoped
public class ResumeResource {

    @Inject ResponseResumer responseResumer;

    @POST
    @Produces(MediaType.APPLICATION_JSON)
    @Consumes(MediaType.APPLICATION_JSON)
    public List<String> check(List<String> conversationIds) {
        return responseResumer.check(conversationIds);
    }
}
