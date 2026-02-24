package io.github.chirino.memory.api;

import io.github.chirino.memory.api.dto.AddOrganizationMemberRequest;
import io.github.chirino.memory.api.dto.CreateOrganizationRequest;
import io.github.chirino.memory.api.dto.CreateTeamRequest;
import io.github.chirino.memory.api.dto.OrganizationDto;
import io.github.chirino.memory.api.dto.OrganizationMemberDto;
import io.github.chirino.memory.api.dto.TeamDto;
import io.github.chirino.memory.api.dto.TeamMemberDto;
import io.github.chirino.memory.client.model.ErrorResponse;
import io.github.chirino.memory.config.MemoryStoreSelector;
import io.github.chirino.memory.store.AccessDeniedException;
import io.github.chirino.memory.store.MemoryStore;
import io.github.chirino.memory.store.ResourceNotFoundException;
import io.quarkus.security.Authenticated;
import io.quarkus.security.identity.SecurityIdentity;
import jakarta.inject.Inject;
import jakarta.ws.rs.Consumes;
import jakarta.ws.rs.DELETE;
import jakarta.ws.rs.GET;
import jakarta.ws.rs.PATCH;
import jakarta.ws.rs.POST;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.PathParam;
import jakarta.ws.rs.Produces;
import jakarta.ws.rs.core.MediaType;
import jakarta.ws.rs.core.Response;
import java.util.List;
import java.util.Map;
import org.jboss.logging.Logger;

@Path("/v1/organizations")
@Authenticated
@Produces(MediaType.APPLICATION_JSON)
@Consumes(MediaType.APPLICATION_JSON)
public class OrganizationsResource {

    private static final Logger LOG = Logger.getLogger(OrganizationsResource.class);

    @Inject MemoryStoreSelector storeSelector;

    @Inject SecurityIdentity identity;

    private MemoryStore store() {
        return storeSelector.getStore();
    }

    private String currentUserId() {
        return identity.getPrincipal().getName();
    }

    // ---- Organization CRUD ----

    @POST
    public Response createOrganization(CreateOrganizationRequest request) {
        try {
            OrganizationDto dto = store().createOrganization(currentUserId(), request);
            return Response.status(Response.Status.CREATED).entity(dto).build();
        } catch (AccessDeniedException e) {
            return forbidden(e);
        }
    }

    @GET
    public Response listOrganizations() {
        List<OrganizationDto> orgs = store().listOrganizations(currentUserId());
        return Response.ok(orgs).build();
    }

    @GET
    @Path("/{orgId}")
    public Response getOrganization(@PathParam("orgId") String orgId) {
        try {
            OrganizationDto dto = store().getOrganization(currentUserId(), orgId);
            return Response.ok(dto).build();
        } catch (ResourceNotFoundException e) {
            return notFound(e);
        } catch (AccessDeniedException e) {
            return forbidden(e);
        }
    }

    @PATCH
    @Path("/{orgId}")
    public Response updateOrganization(@PathParam("orgId") String orgId, Map<String, String> body) {
        try {
            OrganizationDto dto =
                    store().updateOrganization(
                                    currentUserId(), orgId, body.get("name"), body.get("slug"));
            return Response.ok(dto).build();
        } catch (ResourceNotFoundException e) {
            return notFound(e);
        } catch (AccessDeniedException e) {
            return forbidden(e);
        }
    }

    @DELETE
    @Path("/{orgId}")
    public Response deleteOrganization(@PathParam("orgId") String orgId) {
        try {
            store().deleteOrganization(currentUserId(), orgId);
            return Response.noContent().build();
        } catch (ResourceNotFoundException e) {
            return notFound(e);
        } catch (AccessDeniedException e) {
            return forbidden(e);
        }
    }

    // ---- Organization Members ----

    @GET
    @Path("/{orgId}/members")
    public Response listOrgMembers(@PathParam("orgId") String orgId) {
        try {
            List<OrganizationMemberDto> members = store().listOrgMembers(currentUserId(), orgId);
            return Response.ok(members).build();
        } catch (ResourceNotFoundException e) {
            return notFound(e);
        } catch (AccessDeniedException e) {
            return forbidden(e);
        }
    }

    @POST
    @Path("/{orgId}/members")
    public Response addOrgMember(
            @PathParam("orgId") String orgId, AddOrganizationMemberRequest request) {
        try {
            OrganizationMemberDto dto = store().addOrgMember(currentUserId(), orgId, request);
            return Response.status(Response.Status.CREATED).entity(dto).build();
        } catch (ResourceNotFoundException e) {
            return notFound(e);
        } catch (AccessDeniedException e) {
            return forbidden(e);
        }
    }

    @PATCH
    @Path("/{orgId}/members/{userId}")
    public Response updateOrgMemberRole(
            @PathParam("orgId") String orgId,
            @PathParam("userId") String userId,
            Map<String, String> body) {
        try {
            OrganizationMemberDto dto =
                    store().updateOrgMemberRole(currentUserId(), orgId, userId, body.get("role"));
            return Response.ok(dto).build();
        } catch (ResourceNotFoundException e) {
            return notFound(e);
        } catch (AccessDeniedException e) {
            return forbidden(e);
        }
    }

    @DELETE
    @Path("/{orgId}/members/{userId}")
    public Response removeOrgMember(
            @PathParam("orgId") String orgId, @PathParam("userId") String userId) {
        try {
            store().removeOrgMember(currentUserId(), orgId, userId);
            return Response.noContent().build();
        } catch (ResourceNotFoundException e) {
            return notFound(e);
        } catch (AccessDeniedException e) {
            return forbidden(e);
        }
    }

    // ---- Teams ----

    @GET
    @Path("/{orgId}/teams")
    public Response listTeams(@PathParam("orgId") String orgId) {
        try {
            List<TeamDto> teams = store().listTeams(currentUserId(), orgId);
            return Response.ok(teams).build();
        } catch (ResourceNotFoundException e) {
            return notFound(e);
        } catch (AccessDeniedException e) {
            return forbidden(e);
        }
    }

    @POST
    @Path("/{orgId}/teams")
    public Response createTeam(@PathParam("orgId") String orgId, CreateTeamRequest request) {
        try {
            TeamDto dto = store().createTeam(currentUserId(), orgId, request);
            return Response.status(Response.Status.CREATED).entity(dto).build();
        } catch (ResourceNotFoundException e) {
            return notFound(e);
        } catch (AccessDeniedException e) {
            return forbidden(e);
        }
    }

    @GET
    @Path("/{orgId}/teams/{teamId}")
    public Response getTeam(@PathParam("orgId") String orgId, @PathParam("teamId") String teamId) {
        try {
            TeamDto dto = store().getTeam(currentUserId(), orgId, teamId);
            return Response.ok(dto).build();
        } catch (ResourceNotFoundException e) {
            return notFound(e);
        } catch (AccessDeniedException e) {
            return forbidden(e);
        }
    }

    @PATCH
    @Path("/{orgId}/teams/{teamId}")
    public Response updateTeam(
            @PathParam("orgId") String orgId,
            @PathParam("teamId") String teamId,
            Map<String, String> body) {
        try {
            TeamDto dto =
                    store().updateTeam(
                                    currentUserId(),
                                    orgId,
                                    teamId,
                                    body.get("name"),
                                    body.get("slug"));
            return Response.ok(dto).build();
        } catch (ResourceNotFoundException e) {
            return notFound(e);
        } catch (AccessDeniedException e) {
            return forbidden(e);
        }
    }

    @DELETE
    @Path("/{orgId}/teams/{teamId}")
    public Response deleteTeam(
            @PathParam("orgId") String orgId, @PathParam("teamId") String teamId) {
        try {
            store().deleteTeam(currentUserId(), orgId, teamId);
            return Response.noContent().build();
        } catch (ResourceNotFoundException e) {
            return notFound(e);
        } catch (AccessDeniedException e) {
            return forbidden(e);
        }
    }

    // ---- Team Members ----

    @GET
    @Path("/{orgId}/teams/{teamId}/members")
    public Response listTeamMembers(
            @PathParam("orgId") String orgId, @PathParam("teamId") String teamId) {
        try {
            List<TeamMemberDto> members = store().listTeamMembers(currentUserId(), orgId, teamId);
            return Response.ok(members).build();
        } catch (ResourceNotFoundException e) {
            return notFound(e);
        } catch (AccessDeniedException e) {
            return forbidden(e);
        }
    }

    @POST
    @Path("/{orgId}/teams/{teamId}/members")
    public Response addTeamMember(
            @PathParam("orgId") String orgId,
            @PathParam("teamId") String teamId,
            Map<String, String> body) {
        try {
            TeamMemberDto dto =
                    store().addTeamMember(currentUserId(), orgId, teamId, body.get("userId"));
            return Response.status(Response.Status.CREATED).entity(dto).build();
        } catch (ResourceNotFoundException e) {
            return notFound(e);
        } catch (AccessDeniedException e) {
            return forbidden(e);
        }
    }

    @DELETE
    @Path("/{orgId}/teams/{teamId}/members/{userId}")
    public Response removeTeamMember(
            @PathParam("orgId") String orgId,
            @PathParam("teamId") String teamId,
            @PathParam("userId") String userId) {
        try {
            store().removeTeamMember(currentUserId(), orgId, teamId, userId);
            return Response.noContent().build();
        } catch (ResourceNotFoundException e) {
            return notFound(e);
        } catch (AccessDeniedException e) {
            return forbidden(e);
        }
    }

    // ---- Error helpers ----

    private Response notFound(ResourceNotFoundException e) {
        ErrorResponse error = new ErrorResponse();
        error.setError("Not found");
        error.setCode("not_found");
        error.setDetails(Map.of("resource", e.getResource(), "id", e.getId()));
        return Response.status(Response.Status.NOT_FOUND).entity(error).build();
    }

    private Response forbidden(AccessDeniedException e) {
        LOG.infof("Access denied for user=%s: %s", currentUserId(), e.getMessage());
        ErrorResponse error = new ErrorResponse();
        error.setError("Forbidden");
        error.setCode("forbidden");
        error.setDetails(Map.of("message", e.getMessage()));
        return Response.status(Response.Status.FORBIDDEN).entity(error).build();
    }
}
