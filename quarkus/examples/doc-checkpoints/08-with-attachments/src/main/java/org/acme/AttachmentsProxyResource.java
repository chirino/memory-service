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
import org.jboss.resteasy.reactive.server.multipart.MultipartFormDataInput;

/**
 * JAX-RS resource that proxies attachment requests to the memory-service.
 * Delegates all logic to {@link MemoryServiceProxy}.
 */
@Path("/v1/attachments")
@ApplicationScoped
@Blocking
public class AttachmentsProxyResource {

    @Inject MemoryServiceProxy proxy;

    @POST
    @Consumes(MediaType.MULTIPART_FORM_DATA)
    @Produces(MediaType.APPLICATION_JSON)
    public Response upload(
            MultipartFormDataInput input, @QueryParam("expiresIn") String expiresIn) {
        return proxy.uploadAttachment(input, expiresIn);
    }

    @GET
    @Path("/{id}")
    public Response retrieve(@PathParam("id") String id) {
        return proxy.retrieveAttachment(id);
    }

    @GET
    @Path("/{id}/download-url")
    @Produces(MediaType.APPLICATION_JSON)
    public Response getDownloadUrl(@PathParam("id") String id) {
        return proxy.getAttachmentDownloadUrl(id);
    }

    @DELETE
    @Path("/{id}")
    public Response delete(@PathParam("id") String id) {
        return proxy.deleteAttachment(id);
    }
}
