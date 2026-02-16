package io.github.chirino.memory.runtime;

import static io.github.chirino.memory.security.SecurityHelper.bearerToken;
import static jakarta.ws.rs.core.Response.Status.CREATED;
import static jakarta.ws.rs.core.Response.Status.NO_CONTENT;
import static jakarta.ws.rs.core.Response.Status.OK;

import com.fasterxml.jackson.databind.ObjectMapper;
import io.github.chirino.memory.client.api.ConversationsApi;
import io.github.chirino.memory.client.api.SearchApi;
import io.github.chirino.memory.client.api.SharingApi;
import io.github.chirino.memory.client.model.Channel;
import io.github.chirino.memory.client.model.CreateConversationRequest;
import io.github.chirino.memory.client.model.CreateEntryRequest;
import io.github.chirino.memory.client.model.CreateOwnershipTransferRequest;
import io.github.chirino.memory.client.model.IndexEntryRequest;
import io.github.chirino.memory.client.model.SearchConversationsRequest;
import io.github.chirino.memory.client.model.ShareConversationRequest;
import io.github.chirino.memory.client.model.UpdateConversationMembershipRequest;
import io.github.chirino.memory.history.runtime.AttachmentResolver;
import io.quarkus.security.identity.SecurityIdentity;
import jakarta.inject.Inject;
import jakarta.ws.rs.client.Client;
import jakarta.ws.rs.client.ClientBuilder;
import jakarta.ws.rs.core.MediaType;
import jakarta.ws.rs.core.Response;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.HttpURLConnection;
import java.net.URI;
import java.nio.charset.StandardCharsets;
import java.util.List;
import java.util.Map;
import java.util.UUID;
import java.util.function.Supplier;
import org.jboss.logging.Logger;
import org.jboss.resteasy.reactive.server.multipart.FormValue;
import org.jboss.resteasy.reactive.server.multipart.MultipartFormDataInput;

/**
 * Helper class to make it easier to implement a JAXRS proxy to the memory service apis.
 */
public class MemoryServiceProxy {

    private static final Logger LOG = Logger.getLogger(MemoryServiceProxy.class);
    private static final ObjectMapper OBJECT_MAPPER = new ObjectMapper();

    private static UUID toUuid(String s) {
        return s == null || s.isBlank() ? null : UUID.fromString(s);
    }

    @Inject MemoryServiceApiBuilder memoryServiceApiBuilder;

    @Inject SecurityIdentity securityIdentity;

    public Response listConversations(String mode, String after, Integer limit, String query) {
        return execute(
                () -> conversationsApi().listConversations(mode, toUuid(after), limit, query),
                OK,
                "Error listing conversations");
    }

    public Response getConversation(String conversationId) {
        return execute(
                () -> conversationsApi().getConversation(toUuid(conversationId)),
                OK,
                "Error getting history %s",
                conversationId);
    }

    public Response deleteConversation(String conversationId) {
        return executeVoid(
                () -> conversationsApi().deleteConversation(toUuid(conversationId)),
                NO_CONTENT,
                "Error deleting history %s",
                conversationId);
    }

    public Response listConversationEntries(
            String conversationId,
            String after,
            Integer limit,
            Channel channel,
            String epoch,
            String forks) {
        return execute(
                () ->
                        conversationsApi()
                                .listConversationEntries(
                                        toUuid(conversationId),
                                        toUuid(after),
                                        limit,
                                        channel,
                                        epoch,
                                        forks),
                OK,
                "Error listing entries for history %s",
                conversationId);
    }

    public Response listConversationForks(String conversationId) {
        return execute(
                () -> conversationsApi().listConversationForks(toUuid(conversationId)),
                OK,
                "Error listing forks for history %s",
                conversationId);
    }

    public Response shareConversation(String conversationId, String body) {
        try {
            ShareConversationRequest request =
                    OBJECT_MAPPER.readValue(body, ShareConversationRequest.class);
            return execute(
                    () -> sharingApi().shareConversation(toUuid(conversationId), request),
                    CREATED,
                    "Error sharing history %s",
                    conversationId);
        } catch (Exception e) {
            LOG.errorf(e, "Error parsing share request body");
            return handleException(e);
        }
    }

    public Response cancelResponse(String conversationId) {
        return executeVoid(
                () -> conversationsApi().deleteConversationResponse(toUuid(conversationId)),
                OK,
                "Error cancelling response for history %s",
                conversationId);
    }

    public Response createConversation(String body) {
        try {
            CreateConversationRequest request =
                    OBJECT_MAPPER.readValue(body, CreateConversationRequest.class);
            return execute(
                    () -> conversationsApi().createConversation(request),
                    CREATED,
                    "Error creating history");
        } catch (Exception e) {
            LOG.errorf(e, "Error parsing create history request body");
            return handleException(e);
        }
    }

    public Response appendConversationEntry(String conversationId, String body) {
        try {
            CreateEntryRequest request = OBJECT_MAPPER.readValue(body, CreateEntryRequest.class);
            return execute(
                    () ->
                            conversationsApi()
                                    .appendConversationEntry(toUuid(conversationId), request),
                    CREATED,
                    "Error appending entry to history %s",
                    conversationId);
        } catch (Exception e) {
            LOG.errorf(e, "Error parsing append entry request body");
            return handleException(e);
        }
    }

    public Response listConversationMemberships(String conversationId) {
        return execute(
                () -> sharingApi().listConversationMemberships(toUuid(conversationId)),
                OK,
                "Error listing memberships for history %s",
                conversationId);
    }

    public Response updateConversationMembership(
            String conversationId, String userId, String body) {
        try {
            UpdateConversationMembershipRequest request =
                    OBJECT_MAPPER.readValue(body, UpdateConversationMembershipRequest.class);
            return execute(
                    () ->
                            sharingApi()
                                    .updateConversationMembership(
                                            toUuid(conversationId), userId, request),
                    OK,
                    "Error updating membership for history %s, user %s",
                    conversationId,
                    userId);
        } catch (Exception e) {
            LOG.errorf(e, "Error parsing update membership request body");
            return handleException(e);
        }
    }

    public Response deleteConversationMembership(String conversationId, String userId) {
        return executeVoid(
                () -> sharingApi().deleteConversationMembership(toUuid(conversationId), userId),
                NO_CONTENT,
                "Error deleting membership for history %s, user %s",
                conversationId,
                userId);
    }

    public Response listPendingTransfers(String role) {
        return execute(
                () -> sharingApi().listPendingTransfers(role),
                OK,
                "Error listing pending transfers");
    }

    public Response createOwnershipTransfer(String body) {
        try {
            CreateOwnershipTransferRequest request =
                    OBJECT_MAPPER.readValue(body, CreateOwnershipTransferRequest.class);
            return execute(
                    () -> sharingApi().createOwnershipTransfer(request),
                    CREATED,
                    "Error creating ownership transfer");
        } catch (Exception e) {
            LOG.errorf(e, "Error parsing create transfer request body");
            return handleException(e);
        }
    }

    public Response getTransfer(String transferId) {
        return execute(
                () -> sharingApi().getTransfer(toUuid(transferId)),
                OK,
                "Error getting transfer %s",
                transferId);
    }

    public Response acceptTransfer(String transferId) {
        return execute(
                () -> sharingApi().acceptTransfer(toUuid(transferId)),
                OK,
                "Error accepting transfer %s",
                transferId);
    }

    public Response deleteTransfer(String transferId) {
        return executeVoid(
                () -> sharingApi().deleteTransfer(toUuid(transferId)),
                NO_CONTENT,
                "Error deleting transfer %s",
                transferId);
    }

    public Response indexConversations(String body) {
        try {
            List<IndexEntryRequest> request =
                    OBJECT_MAPPER.readValue(
                            body,
                            OBJECT_MAPPER
                                    .getTypeFactory()
                                    .constructCollectionType(List.class, IndexEntryRequest.class));
            return execute(
                    () -> searchApi().indexConversations(request), OK, "Error indexing entries");
        } catch (Exception e) {
            LOG.errorf(e, "Error parsing index entries request body");
            return handleException(e);
        }
    }

    public Response searchConversations(String body) {
        try {
            SearchConversationsRequest request =
                    OBJECT_MAPPER.readValue(body, SearchConversationsRequest.class);
            return execute(
                    () -> searchApi().searchConversations(request),
                    OK,
                    "Error searching conversations");
        } catch (Exception e) {
            LOG.errorf(e, "Error parsing search request body");
            return handleException(e);
        }
    }

    // ---- Attachment operations (raw HTTP, no generated client) ----

    /**
     * Creates an attachment from a source URL by forwarding the JSON request to the memory service.
     */
    public Response createAttachmentFromUrl(Map<String, Object> request) {
        Object ct = request.get("contentType");
        String reqContentType = ct instanceof String ? (String) ct : null;
        if (reqContentType != null && !AttachmentResolver.isSupportedContentType(reqContentType)) {
            return Response.status(Response.Status.BAD_REQUEST)
                    .type(MediaType.APPLICATION_JSON)
                    .entity(Map.of("error", "Unsupported file type: " + reqContentType))
                    .build();
        }
        try {
            String url = memoryServiceApiBuilder.getBaseUrl() + "/v1/attachments";
            String jsonBody = OBJECT_MAPPER.writeValueAsString(request);

            HttpURLConnection conn = (HttpURLConnection) URI.create(url).toURL().openConnection();
            conn.setDoOutput(true);
            conn.setRequestMethod("POST");
            conn.setRequestProperty("Content-Type", "application/json");

            String bearer = bearerToken(securityIdentity);
            if (bearer != null) {
                conn.setRequestProperty("Authorization", "Bearer " + bearer);
            }

            try (OutputStream out = conn.getOutputStream()) {
                out.write(jsonBody.getBytes(StandardCharsets.UTF_8));
                out.flush();
            }

            int status = conn.getResponseCode();
            String responseBody;
            try (InputStream respStream =
                    status >= 400 ? conn.getErrorStream() : conn.getInputStream()) {
                responseBody =
                        respStream != null
                                ? new String(respStream.readAllBytes(), StandardCharsets.UTF_8)
                                : "";
            }
            conn.disconnect();

            return Response.status(status)
                    .type(MediaType.APPLICATION_JSON)
                    .entity(responseBody)
                    .build();
        } catch (Exception e) {
            LOG.errorf(e, "Error creating attachment from URL");
            return Response.status(Response.Status.INTERNAL_SERVER_ERROR)
                    .entity(Map.of("error", "Create from URL proxy failed: " + e.getMessage()))
                    .build();
        }
    }

    /**
     * Uploads an attachment to the memory service using streaming multipart.
     *
     * @param input      the multipart form data containing a "file" field
     * @param expiresIn  optional expiration duration (e.g. "300s")
     * @return the upstream JSON response with attachment metadata
     */
    public Response uploadAttachment(MultipartFormDataInput input, String expiresIn) {
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

            StringBuilder url =
                    new StringBuilder(memoryServiceApiBuilder.getBaseUrl())
                            .append("/v1/attachments");
            if (expiresIn != null && !expiresIn.isBlank()) {
                url.append("?expiresIn=").append(expiresIn);
            }

            String boundary = "----Boundary" + UUID.randomUUID().toString().replace("-", "");
            HttpURLConnection conn =
                    (HttpURLConnection) URI.create(url.toString()).toURL().openConnection();
            conn.setDoOutput(true);
            conn.setRequestMethod("POST");
            conn.setRequestProperty("Content-Type", "multipart/form-data; boundary=" + boundary);
            conn.setChunkedStreamingMode(8192);

            String bearer = bearerToken(securityIdentity);
            if (bearer != null) {
                conn.setRequestProperty("Authorization", "Bearer " + bearer);
            }

            try (OutputStream out = conn.getOutputStream()) {
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
                out.write(partHeader.getBytes(StandardCharsets.UTF_8));

                byte[] buf = new byte[8192];
                int n;
                while ((n = fileStream.read(buf)) != -1) {
                    out.write(buf, 0, n);
                }

                String partFooter = "\r\n--" + boundary + "--\r\n";
                out.write(partFooter.getBytes(StandardCharsets.UTF_8));
                out.flush();
            }

            int status = conn.getResponseCode();
            String responseBody;
            try (InputStream respStream =
                    status >= 400 ? conn.getErrorStream() : conn.getInputStream()) {
                responseBody =
                        respStream != null
                                ? new String(respStream.readAllBytes(), StandardCharsets.UTF_8)
                                : "";
            }
            conn.disconnect();

            return Response.status(status)
                    .type(MediaType.APPLICATION_JSON)
                    .entity(responseBody)
                    .build();
        } catch (Exception e) {
            LOG.errorf(e, "Error uploading attachment");
            return Response.status(Response.Status.INTERNAL_SERVER_ERROR)
                    .entity(Map.of("error", "Upload proxy failed: " + e.getMessage()))
                    .build();
        }
    }

    /**
     * Retrieves an attachment by ID. Handles 302 redirects (e.g. S3 presigned URLs).
     */
    public Response retrieveAttachment(String id) {
        Client client = ClientBuilder.newClient();
        try {
            String url = memoryServiceApiBuilder.getBaseUrl() + "/v1/attachments/" + id;
            jakarta.ws.rs.client.Invocation.Builder req = client.target(url).request();

            String bearer = bearerToken(securityIdentity);
            if (bearer != null) {
                req = req.header("Authorization", "Bearer " + bearer);
            }

            Response upstream = req.get();

            if (upstream.getStatus() == 302) {
                String location = upstream.getHeaderString("Location");
                return Response.temporaryRedirect(URI.create(location)).build();
            }

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

            return Response.status(upstream.getStatus())
                    .type(MediaType.APPLICATION_JSON)
                    .entity(upstream.readEntity(String.class))
                    .build();
        } finally {
            client.close();
        }
    }

    /**
     * Gets a signed download URL for an attachment.
     */
    public Response getAttachmentDownloadUrl(String id) {
        Client client = ClientBuilder.newClient();
        try {
            String url =
                    memoryServiceApiBuilder.getBaseUrl()
                            + "/v1/attachments/"
                            + id
                            + "/download-url";
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

    /**
     * Deletes an attachment by ID.
     */
    public Response deleteAttachment(String id) {
        Client client = ClientBuilder.newClient();
        try {
            String url = memoryServiceApiBuilder.getBaseUrl() + "/v1/attachments/" + id;
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

    /**
     * Downloads an attachment using a signed token. No authentication required.
     */
    public Response downloadAttachmentByToken(String token, String filename) {
        Client client = ClientBuilder.newClient();
        try {
            String url =
                    memoryServiceApiBuilder.getBaseUrl()
                            + "/v1/attachments/download/"
                            + token
                            + "/"
                            + filename;
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
                    .entity(upstream.readEntity(String.class))
                    .build();
        } finally {
            client.close();
        }
    }

    // ---- Private helpers ----

    /**
     * Helper method that executes an API call with proper error handling and security
     * identity propagation.
     *
     * @param apiCall  The API call to execute
     * @param status   The HTTP status code to return on success
     * @param errorMsg Error message format string (supports String.format placeholders)
     * @param args     Arguments for the error message format string
     * @return Response with the API call result
     */
    private <T> Response execute(
            Supplier<T> apiCall, Response.Status status, String errorMsg, Object... args) {
        try {
            T result = apiCall.get();
            Response.ResponseBuilder builder = Response.status(status);
            if (result != null) {
                builder.entity(result);
            }
            return builder.build();
        } catch (Exception e) {
            LOG.errorf(e, errorMsg, args);
            return handleException(e);
        }
    }

    /**
     * Helper method for API calls that return void (e.g., DELETE operations).
     *
     * @param apiCall  The API call to execute
     * @param status   The HTTP status code to return on success
     * @param errorMsg Error message format string (supports String.format placeholders)
     * @param args     Arguments for the error message format string
     * @return Response with the specified status code
     */
    private Response executeVoid(
            Runnable apiCall, Response.Status status, String errorMsg, Object... args) {
        try {
            apiCall.run();
            return Response.status(status).build();
        } catch (Exception e) {
            LOG.errorf(e, errorMsg, args);
            return handleException(e);
        }
    }

    private ConversationsApi conversationsApi() {
        String bearerToken = bearerToken(securityIdentity);
        return memoryServiceApiBuilder.withBearerAuth(bearerToken).build(ConversationsApi.class);
    }

    private SharingApi sharingApi() {
        String bearerToken = bearerToken(securityIdentity);
        return memoryServiceApiBuilder.withBearerAuth(bearerToken).build(SharingApi.class);
    }

    private SearchApi searchApi() {
        String bearerToken = bearerToken(securityIdentity);
        return memoryServiceApiBuilder.withBearerAuth(bearerToken).build(SearchApi.class);
    }

    private Response handleException(Exception e) {
        if (e instanceof jakarta.ws.rs.WebApplicationException wae) {
            int status = wae.getResponse().getStatus();
            String body = "";
            try {
                if (wae.getResponse().hasEntity()) {
                    body = wae.getResponse().readEntity(String.class);
                }
            } catch (Exception ignored) {
            }
            if (status >= 400) {
                LOG.warnf("memory-service call failed: %d %s", status, body);
            }
            Response.ResponseBuilder builder = Response.status(status);
            if (!body.isEmpty()) {
                builder.entity(body);
            }
            return builder.build();
        }
        LOG.errorf(e, "Unexpected error");
        return Response.status(Response.Status.INTERNAL_SERVER_ERROR)
                .entity(Map.of("error", "Internal server error"))
                .build();
    }
}
