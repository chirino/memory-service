package io.github.chirino.memory.api;

import io.github.chirino.memory.admin.client.model.AdminActionRequest;
import io.github.chirino.memory.admin.client.model.AdminAttachment;
import io.github.chirino.memory.admin.client.model.AdminDownloadUrlResponse;
import io.github.chirino.memory.admin.client.model.ErrorResponse;
import io.github.chirino.memory.attachment.AttachmentDeletionService;
import io.github.chirino.memory.attachment.AttachmentDto;
import io.github.chirino.memory.attachment.AttachmentStore;
import io.github.chirino.memory.attachment.AttachmentStoreSelector;
import io.github.chirino.memory.attachment.DownloadUrlSigner;
import io.github.chirino.memory.attachment.FileStore;
import io.github.chirino.memory.attachment.FileStoreException;
import io.github.chirino.memory.attachment.FileStoreSelector;
import io.github.chirino.memory.config.AttachmentConfig;
import io.github.chirino.memory.model.AdminAttachmentQuery;
import io.github.chirino.memory.security.AdminAuditLogger;
import io.github.chirino.memory.security.AdminRoleResolver;
import io.github.chirino.memory.security.ApiKeyContext;
import io.github.chirino.memory.security.JustificationRequiredException;
import io.github.chirino.memory.store.AccessDeniedException;
import io.github.chirino.memory.store.ResourceNotFoundException;
import io.quarkus.security.Authenticated;
import io.quarkus.security.identity.SecurityIdentity;
import jakarta.inject.Inject;
import jakarta.ws.rs.Consumes;
import jakarta.ws.rs.DELETE;
import jakarta.ws.rs.GET;
import jakarta.ws.rs.HeaderParam;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.PathParam;
import jakarta.ws.rs.Produces;
import jakarta.ws.rs.QueryParam;
import jakarta.ws.rs.core.MediaType;
import jakarta.ws.rs.core.Response;
import java.io.InputStream;
import java.net.URI;
import java.net.URLEncoder;
import java.nio.charset.StandardCharsets;
import java.time.Duration;
import java.time.ZoneOffset;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import java.util.UUID;
import org.jboss.logging.Logger;

@Path("/v1/admin/attachments")
@Authenticated
@Produces(MediaType.APPLICATION_JSON)
@Consumes(MediaType.APPLICATION_JSON)
public class AdminAttachmentsResource {

    private static final Logger LOG = Logger.getLogger(AdminAttachmentsResource.class);

    @Inject AttachmentStoreSelector attachmentStoreSelector;

    @Inject FileStoreSelector fileStoreSelector;

    @Inject SecurityIdentity identity;

    @Inject ApiKeyContext apiKeyContext;

    @Inject AdminRoleResolver roleResolver;

    @Inject AdminAuditLogger auditLogger;

    @Inject AttachmentDeletionService deletionService;

    @Inject AttachmentConfig config;

    @Inject DownloadUrlSigner downloadUrlSigner;

    private AttachmentStore store() {
        return attachmentStoreSelector.getStore();
    }

    private FileStore fileStore() {
        return fileStoreSelector.getFileStore();
    }

    @GET
    public Response listAttachments(
            @QueryParam("userId") String userId,
            @QueryParam("entryId") String entryId,
            @QueryParam("status") String status,
            @QueryParam("after") String after,
            @QueryParam("limit") Integer limit,
            @QueryParam("justification") String justification) {
        try {
            roleResolver.requireAuditor(identity, apiKeyContext);
            Map<String, Object> params = new HashMap<>();
            params.put("userId", userId);
            params.put("entryId", entryId);
            params.put("status", status);
            auditLogger.logRead("listAttachments", params, justification, identity, apiKeyContext);

            AdminAttachmentQuery query = new AdminAttachmentQuery();
            query.setUserId(userId);
            query.setEntryId(entryId);
            if (status != null && !status.isBlank()) {
                query.setStatus(AdminAttachmentQuery.AttachmentStatus.fromString(status));
            }
            query.setAfter(after);
            query.setLimit(limit != null ? limit : 50);

            List<AttachmentDto> internal = store().adminList(query);
            List<AdminAttachment> data = internal.stream().map(this::toAdminAttachment).toList();

            Map<String, Object> response = new HashMap<>();
            response.put("data", data);
            if (data.size() == query.getLimit()) {
                response.put("nextCursor", data.get(data.size() - 1).getId().toString());
            } else {
                response.put("nextCursor", null);
            }
            return Response.ok(response).build();
        } catch (AccessDeniedException e) {
            return forbidden(e);
        } catch (JustificationRequiredException e) {
            return justificationRequired();
        } catch (IllegalArgumentException e) {
            return badRequest(e.getMessage());
        }
    }

    @GET
    @Path("/{id}")
    public Response getAttachment(
            @PathParam("id") String id, @QueryParam("justification") String justification) {
        try {
            roleResolver.requireAuditor(identity, apiKeyContext);
            Map<String, Object> params = new HashMap<>();
            params.put("id", id);
            auditLogger.logRead("getAttachment", params, justification, identity, apiKeyContext);

            Optional<AttachmentDto> dto = store().adminFindById(id);
            if (dto.isEmpty()) {
                return notFound(new ResourceNotFoundException("attachment", id));
            }

            AdminAttachment result = toAdminAttachment(dto.get());
            // Add refCount for the detail endpoint
            if (dto.get().storageKey() != null) {
                result.setRefCount((int) store().adminCountByStorageKey(dto.get().storageKey()));
            } else {
                result.setRefCount(0);
            }
            return Response.ok(result).build();
        } catch (AccessDeniedException e) {
            return forbidden(e);
        } catch (JustificationRequiredException e) {
            return justificationRequired();
        } catch (IllegalArgumentException e) {
            return badRequest(e.getMessage());
        }
    }

    @GET
    @Path("/{id}/content")
    public Response getAttachmentContent(
            @PathParam("id") String id,
            @QueryParam("justification") String justification,
            @HeaderParam("If-None-Match") String ifNoneMatch) {
        try {
            roleResolver.requireAuditor(identity, apiKeyContext);
            Map<String, Object> params = new HashMap<>();
            params.put("id", id);
            auditLogger.logRead(
                    "getAttachmentContent", params, justification, identity, apiKeyContext);

            Optional<AttachmentDto> optAtt = store().adminFindById(id);
            if (optAtt.isEmpty()) {
                return notFound(new ResourceNotFoundException("attachment", id));
            }

            AttachmentDto att = optAtt.get();
            if (att.storageKey() == null) {
                return notFound(new ResourceNotFoundException("attachment content", id));
            }

            String cacheControl = "private, max-age=86400, immutable";

            // Check conditional GET
            Optional<Response> notModified =
                    AttachmentResponseHelper.checkNotModified(ifNoneMatch, att, cacheControl);
            if (notModified.isPresent()) {
                return notModified.get();
            }

            // Check for signed URL redirect (S3)
            Duration signedUrlExpiry = Duration.ofHours(1);
            Optional<URI> signedUrl = fileStore().getSignedUrl(att.storageKey(), signedUrlExpiry);
            if (signedUrl.isPresent()) {
                Response.ResponseBuilder builder = Response.temporaryRedirect(signedUrl.get());
                AttachmentResponseHelper.addCacheHeaders(
                        builder, att, "private, max-age=" + signedUrlExpiry.toSeconds());
                return builder.build();
            }

            // Stream bytes directly
            InputStream stream = fileStore().retrieve(att.storageKey());
            Response.ResponseBuilder builder =
                    Response.ok(stream).header("Content-Type", att.contentType());
            if (att.size() != null) {
                builder.header("Content-Length", att.size());
            }
            if (att.filename() != null) {
                builder.header(
                        "Content-Disposition", "inline; filename=\"" + att.filename() + "\"");
            }
            AttachmentResponseHelper.addCacheHeaders(builder, att, cacheControl);
            return builder.build();
        } catch (AccessDeniedException e) {
            return forbidden(e);
        } catch (JustificationRequiredException e) {
            return justificationRequired();
        } catch (FileStoreException e) {
            return fileStoreErrorResponse(e);
        } catch (IllegalArgumentException e) {
            return badRequest(e.getMessage());
        }
    }

    @GET
    @Path("/{id}/download-url")
    public Response getDownloadUrl(
            @PathParam("id") String id, @QueryParam("justification") String justification) {
        try {
            roleResolver.requireAuditor(identity, apiKeyContext);
            Map<String, Object> params = new HashMap<>();
            params.put("id", id);
            auditLogger.logRead(
                    "getAttachmentDownloadUrl", params, justification, identity, apiKeyContext);

            Optional<AttachmentDto> optAtt = store().adminFindById(id);
            if (optAtt.isEmpty()) {
                return notFound(new ResourceNotFoundException("attachment", id));
            }

            AttachmentDto att = optAtt.get();
            if (att.storageKey() == null) {
                return notFound(new ResourceNotFoundException("attachment content", id));
            }

            Duration expiry = config.getDownloadUrlExpiresIn();

            // For S3: return the pre-signed URL directly
            Optional<URI> signedUrl = fileStore().getSignedUrl(att.storageKey(), expiry);
            if (signedUrl.isPresent()) {
                AdminDownloadUrlResponse resp = new AdminDownloadUrlResponse();
                resp.setUrl(signedUrl.get().toString());
                resp.setExpiresIn((int) expiry.toSeconds());
                return Response.ok(resp).build();
            }

            // For DB: generate an HMAC-signed token
            String token = downloadUrlSigner.createToken(id, expiry);
            String filename =
                    att.filename() != null
                            ? URLEncoder.encode(att.filename(), StandardCharsets.UTF_8)
                            : "attachment";
            String url = "/v1/attachments/download/" + token + "/" + filename;

            AdminDownloadUrlResponse resp = new AdminDownloadUrlResponse();
            resp.setUrl(url);
            resp.setExpiresIn((int) expiry.toSeconds());
            return Response.ok(resp).build();
        } catch (AccessDeniedException e) {
            return forbidden(e);
        } catch (JustificationRequiredException e) {
            return justificationRequired();
        } catch (IllegalArgumentException e) {
            return badRequest(e.getMessage());
        }
    }

    @DELETE
    @Path("/{id}")
    public Response deleteAttachment(@PathParam("id") String id, AdminActionRequest request) {
        try {
            roleResolver.requireAdmin(identity, apiKeyContext);
            String justification = request != null ? request.getJustification() : null;
            auditLogger.logWrite("deleteAttachment", id, justification, identity, apiKeyContext);

            Optional<AttachmentDto> optAtt = store().adminFindById(id);
            if (optAtt.isEmpty()) {
                return notFound(new ResourceNotFoundException("attachment", id));
            }

            AttachmentDto att = optAtt.get();

            // If linked, unlink first so deletion service can proceed
            if (att.entryId() != null) {
                store().adminUnlinkFromEntry(id);
            }

            // Delete with ref-count safety
            deletionService.deleteAttachment(id);
            return Response.noContent().build();
        } catch (AccessDeniedException e) {
            return forbidden(e);
        } catch (JustificationRequiredException e) {
            return justificationRequired();
        } catch (ResourceNotFoundException e) {
            return notFound(e);
        } catch (IllegalArgumentException e) {
            return badRequest(e.getMessage());
        }
    }

    private AdminAttachment toAdminAttachment(AttachmentDto dto) {
        AdminAttachment result = new AdminAttachment();
        result.setId(dto.id() != null ? UUID.fromString(dto.id()) : null);
        result.setStorageKey(dto.storageKey());
        result.setFilename(dto.filename());
        result.setContentType(dto.contentType());
        result.setSize(dto.size());
        result.setSha256(dto.sha256());
        result.setUserId(dto.userId());
        result.setEntryId(dto.entryId() != null ? UUID.fromString(dto.entryId()) : null);
        result.setExpiresAt(
                dto.expiresAt() != null ? dto.expiresAt().atOffset(ZoneOffset.UTC) : null);
        result.setCreatedAt(
                dto.createdAt() != null ? dto.createdAt().atOffset(ZoneOffset.UTC) : null);
        result.setDeletedAt(
                dto.deletedAt() != null ? dto.deletedAt().atOffset(ZoneOffset.UTC) : null);
        return result;
    }

    private Response notFound(ResourceNotFoundException e) {
        ErrorResponse error = new ErrorResponse();
        error.setError("Not found");
        error.setCode("not_found");
        error.setDetails(Map.of("resource", e.getResource(), "id", e.getId()));
        return Response.status(Response.Status.NOT_FOUND).entity(error).build();
    }

    private Response forbidden(AccessDeniedException e) {
        LOG.infof("Access denied for admin attachment operation: %s", e.getMessage());
        ErrorResponse error = new ErrorResponse();
        error.setError("Forbidden");
        error.setCode("forbidden");
        error.setDetails(Map.of("message", e.getMessage()));
        return Response.status(Response.Status.FORBIDDEN).entity(error).build();
    }

    private Response justificationRequired() {
        ErrorResponse error = new ErrorResponse();
        error.setError("Justification is required for admin operations");
        error.setCode("JUSTIFICATION_REQUIRED");
        return Response.status(Response.Status.BAD_REQUEST).entity(error).build();
    }

    private Response badRequest(String message) {
        ErrorResponse error = new ErrorResponse();
        error.setError("Bad request");
        error.setCode("bad_request");
        error.setDetails(Map.of("message", message));
        return Response.status(Response.Status.BAD_REQUEST).entity(error).build();
    }

    private Response fileStoreErrorResponse(FileStoreException e) {
        Map<String, Object> details = new HashMap<>(e.getDetails());
        details.put("message", e.getMessage());
        ErrorResponse error = new ErrorResponse();
        error.setError(e.getMessage());
        error.setCode(e.getCode());
        error.setDetails(details);
        return Response.status(e.getHttpStatus()).entity(error).build();
    }
}
