package org.acme;

import io.github.chirino.memory.runtime.MemoryServiceProxy;
import io.smallrye.common.annotation.Blocking;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.ws.rs.GET;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.PathParam;
import jakarta.ws.rs.core.Response;

/**
 * Unauthenticated proxy that forwards signed download requests to the memory-service. No bearer
 * token is required — the signed token in the URL path provides authorization.
 */
@Path("/v1/attachments/download")
@ApplicationScoped
@Blocking
public class AttachmentDownloadProxyResource {

    @Inject MemoryServiceProxy proxy;

    @GET
    @Path("/{token}/{filename}")
    public Response download(
            @PathParam("token") String token, @PathParam("filename") String filename) {
        return proxy.downloadAttachmentByToken(token, filename);
    }
}
