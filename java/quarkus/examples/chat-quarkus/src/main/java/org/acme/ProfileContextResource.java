package org.acme;

import com.fasterxml.jackson.annotation.JsonInclude;
import io.github.chirino.memory.client.api.MemoriesApi;
import io.github.chirino.memory.client.model.MemoryItem;
import io.github.chirino.memory.client.model.PutMemoryRequest;
import io.quarkus.security.Authenticated;
import io.smallrye.common.annotation.Blocking;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import jakarta.ws.rs.Consumes;
import jakarta.ws.rs.GET;
import jakarta.ws.rs.InternalServerErrorException;
import jakarta.ws.rs.PUT;
import jakarta.ws.rs.Path;
import jakarta.ws.rs.Produces;
import jakarta.ws.rs.core.MediaType;
import java.util.List;
import java.util.Map;

@Path("/v1/profile-context")
@ApplicationScoped
@Blocking
@Authenticated
public class ProfileContextResource {

    static final String PROFILE_CONTEXT_KEY = "latest";
    static final String PROFILE_INPUT_KEY = "latest";

    @Inject CognitionMemoryAccess access;
    @Inject ProfileContextInputsConfig inputsConfig;

    @GET
    @Produces(MediaType.APPLICATION_JSON)
    public ProfileContextResponse getProfileContext() {
        try {
            String userId = access.userId();
            MemoryItem item =
                    access.memoriesApi()
                            .getMemory(
                                    access.profileContextNamespace(userId),
                                    PROFILE_CONTEXT_KEY,
                                    false,
                                    "exclude");
            Map<String, Object> value = item.getValue();
            return new ProfileContextResponse(
                    true,
                    CognitionMemoryValues.stringValue(value, "generated_at"),
                    CognitionMemoryValues.stringValue(value, "content"),
                    objectMap(value, "sections"),
                    objectList(value, "conflicts"),
                    objectList(value, "omitted"));
        } catch (RuntimeException e) {
            if (CognitionMemoryValues.isHttpNotFound(e)) {
                return ProfileContextResponse.missing();
            }
            throw e;
        }
    }

    @GET
    @Path("/inputs")
    @Produces(MediaType.APPLICATION_JSON)
    public List<String> getProfileInputs() {
        try {
            String userId = access.userId();
            MemoryItem item =
                    access.memoriesApi()
                            .getMemory(
                                    access.profileInputNamespace(userId),
                                    PROFILE_INPUT_KEY,
                                    false,
                                    "exclude");
            return inputsFromValue(item.getValue());
        } catch (RuntimeException e) {
            if (CognitionMemoryValues.isHttpNotFound(e)) {
                return List.of();
            }
            throw e;
        }
    }

    @PUT
    @Path("/inputs")
    @Consumes(MediaType.APPLICATION_JSON)
    @Produces(MediaType.APPLICATION_JSON)
    public List<String> putProfileInputs(List<Object> request) {
        List<String> accepted =
                CognitionMemoryValues.normalizeInputs(
                        request, inputsConfig.maxItems(), inputsConfig.maxItemChars());

        String userId = access.userId();
        MemoriesApi api = access.memoriesApi();

        PutMemoryRequest put = new PutMemoryRequest();
        put.setNamespace(access.profileInputNamespace(userId));
        put.setKey(PROFILE_INPUT_KEY);
        put.setValue(
                Map.of(
                        "kind",
                        "profile_context_inputs",
                        "inputs",
                        accepted,
                        "source",
                        "user",
                        "updated_by",
                        userId));
        api.putMemory(put);
        return accepted;
    }

    @SuppressWarnings("unchecked")
    private static Map<String, Object> objectMap(Map<String, Object> value, String key) {
        Object item = value == null ? null : value.get(key);
        return item instanceof Map<?, ?> map ? (Map<String, Object>) map : Map.of();
    }

    @SuppressWarnings("unchecked")
    private static List<Object> objectList(Map<String, Object> value, String key) {
        Object item = value == null ? null : value.get(key);
        return item instanceof List<?> list ? (List<Object>) list : List.of();
    }

    private static List<String> inputsFromValue(Map<String, Object> value) {
        Object inputs = value == null ? null : value.get("inputs");
        if (!(inputs instanceof List<?> list)) {
            throw new InternalServerErrorException("profile_input/latest has no inputs array");
        }
        for (Object input : list) {
            if (!(input instanceof String)) {
                throw new InternalServerErrorException(
                        "profile_input/latest contains a non-string input");
            }
        }
        return list.stream().map(String.class::cast).toList();
    }

    @JsonInclude(JsonInclude.Include.NON_NULL)
    public record ProfileContextResponse(
            boolean exists,
            String generatedAt,
            String content,
            Map<String, Object> sections,
            List<Object> conflicts,
            List<Object> omitted) {

        static ProfileContextResponse missing() {
            return new ProfileContextResponse(false, null, "", null, null, null);
        }
    }
}
