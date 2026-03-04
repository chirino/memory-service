package org.acme;

import jakarta.ws.rs.GET;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.Produces;
import jakarta.ws.rs.core.MediaType;
import java.util.Map;

@Path("/ready")
public class ReadyResource {

    @GET
    @Produces(MediaType.APPLICATION_JSON)
    public Map<String, String> ready() {
        return Map.of("status", "ok");
    }
}
