package org.acme;

import io.github.chirino.memory.runtime.MemoryServiceProxy;
import io.smallrye.common.annotation.Blocking;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.ws.rs.Consumes;
import jakarta.ws.rs.DELETE;
import jakarta.ws.rs.GET;
import jakarta.ws.rs.POST;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.PathParam;
import jakarta.ws.rs.Produces;
import jakarta.ws.rs.QueryParam;
import jakarta.ws.rs.core.MediaType;
import jakarta.ws.rs.core.Response;

@Path("/v1/ownership-transfers")
@ApplicationScoped
@Blocking
public class OwnershipTransfersResource {

    @Inject MemoryServiceProxy proxy;

    @GET
    @Produces(MediaType.APPLICATION_JSON)
    public Response listPendingTransfers(@QueryParam("role") String role) {
        return proxy.listPendingTransfers(role);
    }

    @POST
    @Consumes(MediaType.APPLICATION_JSON)
    @Produces(MediaType.APPLICATION_JSON)
    public Response createOwnershipTransfer(String body) {
        return proxy.createOwnershipTransfer(body);
    }

    @GET
    @Path("/{transferId}")
    @Produces(MediaType.APPLICATION_JSON)
    public Response getTransfer(@PathParam("transferId") String transferId) {
        return proxy.getTransfer(transferId);
    }

    @POST
    @Path("/{transferId}/accept")
    @Produces(MediaType.APPLICATION_JSON)
    public Response acceptTransfer(@PathParam("transferId") String transferId) {
        return proxy.acceptTransfer(transferId);
    }

    @DELETE
    @Path("/{transferId}")
    public Response deleteTransfer(@PathParam("transferId") String transferId) {
        return proxy.deleteTransfer(transferId);
    }
}
