package example;

import static io.github.chirino.memory.security.SecurityHelper.bearerToken;

import io.quarkus.security.identity.SecurityIdentity;
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
import jakarta.ws.rs.client.Client;
import jakarta.ws.rs.client.ClientBuilder;
import jakarta.ws.rs.core.MediaType;
import jakarta.ws.rs.core.Response;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.HttpURLConnection;
import java.net.URI;
import java.util.Map;
import java.util.Optional;
import java.util.UUID;
import org.eclipse.microprofile.config.inject.ConfigProperty;
import org.jboss.resteasy.reactive.server.multipart.FormValue;
import org.jboss.resteasy.reactive.server.multipart.MultipartFormDataInput;

/**
 * JAX-RS resource that proxies attachment requests to the memory-service. Uses raw HTTP streaming to
 * avoid buffering file content in memory.
 */
@Path("/v1/attachments")
@ApplicationScoped
@Blocking
public class AttachmentsProxyResource {

    @Inject SecurityIdentity securityIdentity;

    @ConfigProperty(name = "memory-service.client.url")
    Optional<String> clientUrl;

    @ConfigProperty(name = "quarkus.rest-client.memory-service-client.url")
    Optional<String> quarkusRestClientUrl;

    private String baseUrl() {
        return clientUrl.orElseGet(() -> quarkusRestClientUrl.orElse("http://localhost:8080"));
    }

    @POST
    @Consumes(MediaType.MULTIPART_FORM_DATA)
    @Produces(MediaType.APPLICATION_JSON)
    public Response upload(
            MultipartFormDataInput input, @QueryParam("expiresIn") String expiresIn) {
        var fileEntry = input.getValues().get("file");
        if (fileEntry == null || fileEntry.isEmpty()) {
            return Response.status(Response.Status.BAD_REQUEST)
                    .entity(Map.of("error", "No file provided"))
                    .build();
        }

        FormValue formValue = fileEntry.iterator().next();
        if (!formValue.isFileItem()) {
            return Response.status(Response.Status.BAD_REQUEST)
                    .entity(Map.of("error", "The 'file' field must be a file upload"))
                    .build();
        }

        String contentType =
                formValue.getHeaders().getFirst("Content-Type") != null
                        ? formValue.getHeaders().getFirst("Content-Type")
                        : "application/octet-stream";
        String filename = formValue.getFileName();

        try {
            InputStream fileStream = formValue.getFileItem().getInputStream();

            // Build the target URL
            StringBuilder url = new StringBuilder(baseUrl()).append("/v1/attachments");
            if (expiresIn != null && !expiresIn.isBlank()) {
                url.append("?expiresIn=").append(expiresIn);
            }

            // Use HttpURLConnection for streaming multipart upload.
            // This streams the file through without buffering the entire content in memory.
            String boundary = "----Boundary" + UUID.randomUUID().toString().replace("-", "");
            HttpURLConnection conn =
                    (HttpURLConnection) URI.create(url.toString()).toURL().openConnection();
            conn.setDoOutput(true);
            conn.setRequestMethod("POST");
            conn.setRequestProperty("Content-Type", "multipart/form-data; boundary=" + boundary);
            // Enable streaming mode to avoid buffering the entire body in memory
            conn.setChunkedStreamingMode(8192);

            String bearer = bearerToken(securityIdentity);
            if (bearer != null) {
                conn.setRequestProperty("Authorization", "Bearer " + bearer);
            }

            // Write multipart body, streaming the file content
            try (OutputStream out = conn.getOutputStream()) {
                // Part header
                String partHeader =
                        "--"
                                + boundary
                                + "\r\n"
                                + "Content-Disposition: form-data; name=\"file\"; filename=\""
                                + (filename != null ? filename : "upload")
                                + "\"\r\n"
                                + "Content-Type: "
                                + contentType
                                + "\r\n\r\n";
                out.write(partHeader.getBytes(java.nio.charset.StandardCharsets.UTF_8));

                // Stream file content in chunks
                byte[] buf = new byte[8192];
                int n;
                while ((n = fileStream.read(buf)) != -1) {
                    out.write(buf, 0, n);
                }

                // Part footer
                String partFooter = "\r\n--" + boundary + "--\r\n";
                out.write(partFooter.getBytes(java.nio.charset.StandardCharsets.UTF_8));
                out.flush();
            }

            int status = conn.getResponseCode();
            String responseBody;
            try (InputStream respStream =
                    status >= 400 ? conn.getErrorStream() : conn.getInputStream()) {
                responseBody =
                        respStream != null
                                ? new String(
                                        respStream.readAllBytes(),
                                        java.nio.charset.StandardCharsets.UTF_8)
                                : "";
            }
            conn.disconnect();

            return Response.status(status)
                    .type(MediaType.APPLICATION_JSON)
                    .entity(responseBody)
                    .build();
        } catch (Exception e) {
            return Response.status(Response.Status.INTERNAL_SERVER_ERROR)
                    .entity(Map.of("error", "Upload proxy failed: " + e.getMessage()))
                    .build();
        }
    }

    @GET
    @Path("/{id}")
    public Response retrieve(@PathParam("id") String id) {
        Client client = ClientBuilder.newClient();
        try {
            String url = baseUrl() + "/v1/attachments/" + id;
            jakarta.ws.rs.client.Invocation.Builder req = client.target(url).request();

            String bearer = bearerToken(securityIdentity);
            if (bearer != null) {
                req = req.header("Authorization", "Bearer " + bearer);
            }

            Response upstream = req.get();

            // Handle redirects (302)
            if (upstream.getStatus() == 302) {
                String location = upstream.getHeaderString("Location");
                return Response.temporaryRedirect(java.net.URI.create(location)).build();
            }

            // Stream the response body through without buffering
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
                return builder.build();
            }

            // Forward error responses
            return Response.status(upstream.getStatus())
                    .type(MediaType.APPLICATION_JSON)
                    .entity(upstream.readEntity(String.class))
                    .build();
        } finally {
            client.close();
        }
    }

    @GET
    @Path("/{id}/download-url")
    @Produces(MediaType.APPLICATION_JSON)
    public Response getDownloadUrl(@PathParam("id") String id) {
        Client client = ClientBuilder.newClient();
        try {
            String url = baseUrl() + "/v1/attachments/" + id + "/download-url";
            jakarta.ws.rs.client.Invocation.Builder req = client.target(url).request();

            String bearer = bearerToken(securityIdentity);
            if (bearer != null) {
                req = req.header("Authorization", "Bearer " + bearer);
            }

            Response upstream = req.get();

            return Response.status(upstream.getStatus())
                    .type(MediaType.APPLICATION_JSON)
                    .entity(upstream.readEntity(String.class))
                    .build();
        } finally {
            client.close();
        }
    }

    @DELETE
    @Path("/{id}")
    public Response delete(@PathParam("id") String id) {
        Client client = ClientBuilder.newClient();
        try {
            String url = baseUrl() + "/v1/attachments/" + id;
            jakarta.ws.rs.client.Invocation.Builder req = client.target(url).request();

            String bearer = bearerToken(securityIdentity);
            if (bearer != null) {
                req = req.header("Authorization", "Bearer " + bearer);
            }

            Response upstream = req.delete();

            if (upstream.getStatus() == 204) {
                return Response.noContent().build();
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
