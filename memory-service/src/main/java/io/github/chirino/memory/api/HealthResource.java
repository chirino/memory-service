package io.github.chirino.memory.api;

import jakarta.ws.rs.GET;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.Produces;
import jakarta.ws.rs.core.MediaType;
import java.util.Collections;
import java.util.Map;

@Path("/v1/health")
public class HealthResource {

    @GET
    @Produces(MediaType.APPLICATION_JSON)
    public Map<String, String> health() {
        return Collections.singletonMap("status", "ok");
    }
}
