package io.github.chirino.memory.api;

import io.github.chirino.memory.api.dto.CreateOwnershipTransferRequest;
import io.github.chirino.memory.api.dto.OwnershipTransferDto;
import io.github.chirino.memory.client.model.ErrorResponse;
import io.github.chirino.memory.config.MemoryStoreSelector;
import io.github.chirino.memory.store.AccessDeniedException;
import io.github.chirino.memory.store.MemoryStore;
import io.github.chirino.memory.store.ResourceConflictException;
import io.github.chirino.memory.store.ResourceNotFoundException;
import io.quarkus.security.Authenticated;
import io.quarkus.security.identity.SecurityIdentity;
import jakarta.inject.Inject;
import jakarta.ws.rs.Consumes;
import jakarta.ws.rs.DELETE;
import jakarta.ws.rs.DefaultValue;
import jakarta.ws.rs.GET;
import jakarta.ws.rs.POST;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.PathParam;
import jakarta.ws.rs.Produces;
import jakarta.ws.rs.QueryParam;
import jakarta.ws.rs.core.MediaType;
import jakarta.ws.rs.core.Response;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.Optional;
import org.jboss.logging.Logger;

@Path("/v1/ownership-transfers")
@Authenticated
@Produces(MediaType.APPLICATION_JSON)
@Consumes(MediaType.APPLICATION_JSON)
public class OwnershipTransfersResource {

    private static final Logger LOG = Logger.getLogger(OwnershipTransfersResource.class);

    @Inject MemoryStoreSelector storeSelector;

    @Inject SecurityIdentity identity;

    private MemoryStore store() {
        return storeSelector.getStore();
    }

    private String currentUserId() {
        return identity.getPrincipal().getName();
    }

    @GET
    public Response listPendingTransfers(@QueryParam("role") @DefaultValue("all") String role) {
        List<OwnershipTransferDto> transfers = store().listPendingTransfers(currentUserId(), role);
        Map<String, Object> response = new HashMap<>();
        response.put("data", transfers);
        return Response.ok(response).build();
    }

    @GET
    @Path("/{transferId}")
    public Response getTransfer(@PathParam("transferId") String transferId) {
        Optional<OwnershipTransferDto> transfer = store().getTransfer(currentUserId(), transferId);
        if (transfer.isEmpty()) {
            return notFound("transfer", transferId);
        }
        return Response.ok(transfer.get()).build();
    }

    @POST
    public Response createOwnershipTransfer(CreateOwnershipTransferRequest request) {
        try {
            OwnershipTransferDto transfer =
                    store().createOwnershipTransfer(currentUserId(), request);
            return Response.status(Response.Status.CREATED).entity(transfer).build();
        } catch (ResourceNotFoundException e) {
            return notFound(e);
        } catch (AccessDeniedException e) {
            return forbidden(e);
        } catch (ResourceConflictException e) {
            return conflictWithExistingTransfer(e);
        } catch (IllegalArgumentException e) {
            return badRequest(e.getMessage());
        }
    }

    @POST
    @Path("/{transferId}/accept")
    public Response acceptTransfer(@PathParam("transferId") String transferId) {
        try {
            store().acceptTransfer(currentUserId(), transferId);
            return Response.noContent().build();
        } catch (ResourceNotFoundException e) {
            return notFound(e);
        } catch (AccessDeniedException e) {
            return forbidden(e);
        }
    }

    @DELETE
    @Path("/{transferId}")
    public Response deleteTransfer(@PathParam("transferId") String transferId) {
        try {
            store().deleteTransfer(currentUserId(), transferId);
            return Response.noContent().build();
        } catch (ResourceNotFoundException e) {
            return notFound(e);
        } catch (AccessDeniedException e) {
            return forbidden(e);
        }
    }

    private Response notFound(String resource, String id) {
        ErrorResponse error = new ErrorResponse();
        error.setError("Not found");
        error.setCode("not_found");
        error.setDetails(Map.of("resource", resource, "id", id));
        return Response.status(Response.Status.NOT_FOUND).entity(error).build();
    }

    private Response notFound(ResourceNotFoundException e) {
        return notFound(e.getResource(), e.getId());
    }

    private Response forbidden(AccessDeniedException e) {
        LOG.infof("Access denied for user=%s: %s", currentUserId(), e.getMessage());
        ErrorResponse error = new ErrorResponse();
        error.setError("Forbidden");
        error.setCode("forbidden");
        error.setDetails(Map.of("message", e.getMessage()));
        return Response.status(Response.Status.FORBIDDEN).entity(error).build();
    }

    private Response conflict(String message) {
        ErrorResponse error = new ErrorResponse();
        error.setError("Conflict");
        error.setCode("conflict");
        error.setDetails(Map.of("message", message));
        return Response.status(Response.Status.CONFLICT).entity(error).build();
    }

    private Response conflictWithExistingTransfer(ResourceConflictException e) {
        Map<String, Object> body = new HashMap<>();
        body.put("error", e.getMessage());
        body.put("code", "TRANSFER_ALREADY_PENDING");
        body.put("existingTransferId", e.getId());
        return Response.status(Response.Status.CONFLICT).entity(body).build();
    }

    private Response badRequest(String message) {
        ErrorResponse error = new ErrorResponse();
        error.setError("Bad request");
        error.setCode("bad_request");
        error.setDetails(Map.of("message", message));
        return Response.status(Response.Status.BAD_REQUEST).entity(error).build();
    }
}
