package org.acme;

import io.github.chirino.memory.runtime.MemoryServiceProxy;
import io.github.chirino.memory.runtime.MemoryServiceProxy.EventNotification;
import io.smallrye.mutiny.Multi;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.ws.rs.GET;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.Produces;
import jakarta.ws.rs.QueryParam;
import jakarta.ws.rs.core.MediaType;

@Path("/v1/events")
@ApplicationScoped
public class EventsResource {

    @Inject MemoryServiceProxy proxy;

    @GET
    @Produces(MediaType.SERVER_SENT_EVENTS)
    public Multi<EventNotification> streamEvents(@QueryParam("kinds") String kinds) {
        return proxy.streamEvents(kinds);
    }
}
