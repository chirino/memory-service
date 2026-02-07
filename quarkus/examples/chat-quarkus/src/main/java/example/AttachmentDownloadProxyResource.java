package example;

import io.smallrye.common.annotation.Blocking;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.ws.rs.GET;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.PathParam;
import jakarta.ws.rs.client.Client;
import jakarta.ws.rs.client.ClientBuilder;
import jakarta.ws.rs.core.MediaType;
import jakarta.ws.rs.core.Response;
import java.io.InputStream;
import java.util.Optional;
import org.eclipse.microprofile.config.inject.ConfigProperty;

/**
 * Unauthenticated proxy that forwards signed download requests to the memory-service. No bearer
 * token is required — the signed token in the URL path provides authorization.
 */
@Path("/v1/attachments/download")
@ApplicationScoped
@Blocking
public class AttachmentDownloadProxyResource {

    @ConfigProperty(name = "memory-service.client.url")
    Optional<String> clientUrl;

    @ConfigProperty(name = "quarkus.rest-client.memory-service-client.url")
    Optional<String> quarkusRestClientUrl;

    private String baseUrl() {
        return clientUrl.orElseGet(() -> quarkusRestClientUrl.orElse("http://localhost:8080"));
    }

    @GET
    @Path("/{token}/{filename}")
    public Response download(
            @PathParam("token") String token, @PathParam("filename") String filename) {
        Client client = ClientBuilder.newClient();
        try {
            String url = baseUrl() + "/v1/attachments/download/" + token + "/" + filename;
            Response upstream = client.target(url).request().get();

            if (upstream.getStatus() == 200) {
                InputStream body = upstream.readEntity(InputStream.class);
                Response.ResponseBuilder builder = Response.ok(body);
                String ct = upstream.getHeaderString("Content-Type");
                if (ct != null) {
                    builder.header("Content-Type", ct);
                }
                String cl = upstream.getHeaderString("Content-Length");
                if (cl != null) {
                    builder.header("Content-Length", cl);
                }
                String cd = upstream.getHeaderString("Content-Disposition");
                if (cd != null) {
                    builder.header("Content-Disposition", cd);
                }
                String cc = upstream.getHeaderString("Cache-Control");
                if (cc != null) {
                    builder.header("Cache-Control", cc);
                }
                return builder.build();
            }

            return Response.status(upstream.getStatus())
                    .type(MediaType.APPLICATION_JSON)
                    .entity(upstream.readEntity(String.class))
                    .build();
        } finally {
            client.close();
        }
    }
}
