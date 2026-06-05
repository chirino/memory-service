package org.acme;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

import io.github.chirino.memory.client.api.MemoriesApi;
import io.github.chirino.memory.client.model.ListMemoryEventsResponse;
import io.github.chirino.memory.client.model.ListMemoryNamespacesResponse;
import io.github.chirino.memory.client.model.MemoryItem;
import io.github.chirino.memory.client.model.MemoryWriteResult;
import io.github.chirino.memory.client.model.PutMemoryRequest;
import io.github.chirino.memory.client.model.SearchMemoriesRequest;
import io.github.chirino.memory.client.model.SearchMemoriesResponse;
import io.github.chirino.memory.client.model.UpdateMemoryRequest;
import jakarta.ws.rs.core.Response;
import java.time.OffsetDateTime;
import java.util.List;
import java.util.Map;
import org.junit.jupiter.api.Test;

class ProfileContextResourceTest {

    @Test
    void profileContextUsesAuthenticatedUserNamespace() {
        FakeMemoriesApi api = new FakeMemoriesApi();
        api.memoryValue = Map.of("generated_at", "2026-06-04T12:00:00Z", "content", "Bob profile");
        ProfileContextResource resource = resource("bob", api);

        ProfileContextResource.ProfileContextResponse response = resource.getProfileContext();

        assertTrue(response.exists());
        assertEquals("Bob profile", response.content());
        assertEquals(
                List.of("user", "bob", "cognition.v1", "profile_context"), api.lastGetNamespace);
        assertEquals("latest", api.lastGetKey);
    }

    @Test
    void profileInputWritesAuthenticatedUserNamespace() {
        FakeMemoriesApi api = new FakeMemoriesApi();
        ProfileContextResource resource = resource("bob", api);

        List<String> accepted =
                resource.putProfileInputs(
                        List.of(
                                " Prefer iterative examples ",
                                "Prefer   iterative examples",
                                "Use Java"));

        assertEquals(List.of("Prefer iterative examples", "Use Java"), accepted);
        assertEquals(
                List.of("user", "bob", "cognition.v1", "profile_input"),
                api.lastPut.getNamespace());
        assertEquals("latest", api.lastPut.getKey());
        assertEquals("bob", api.lastPut.getValue().get("updated_by"));
        assertEquals(accepted, api.lastPut.getValue().get("inputs"));
    }

    private static ProfileContextResource resource(String userId, FakeMemoriesApi api) {
        ProfileContextResource resource = new ProfileContextResource();
        resource.access = new FakeCognitionMemoryAccess(userId, api);
        resource.inputsConfig =
                new ProfileContextInputsConfig() {
                    @Override
                    public int maxItems() {
                        return 50;
                    }

                    @Override
                    public int maxItemChars() {
                        return 1000;
                    }
                };
        return resource;
    }

    private static class FakeCognitionMemoryAccess extends CognitionMemoryAccess {
        private final String userId;
        private final MemoriesApi api;

        FakeCognitionMemoryAccess(String userId, MemoriesApi api) {
            this.userId = userId;
            this.api = api;
        }

        @Override
        public String userId() {
            return userId;
        }

        @Override
        public MemoriesApi memoriesApi() {
            return api;
        }

        @Override
        public List<String> profileContextNamespace(String userId) {
            return List.of("user", userId, "cognition.v1", "profile_context");
        }

        @Override
        public List<String> profileInputNamespace(String userId) {
            return List.of("user", userId, "cognition.v1", "profile_input");
        }
    }

    private static class FakeMemoriesApi implements MemoriesApi {
        Map<String, Object> memoryValue = Map.of("inputs", List.of());
        List<String> lastGetNamespace;
        String lastGetKey;
        PutMemoryRequest lastPut;

        @Override
        public MemoryItem getMemory(
                List<String> ns, String key, Boolean includeUsage, String archived) {
            lastGetNamespace = ns;
            lastGetKey = key;
            MemoryItem item = new MemoryItem();
            item.setNamespace(ns);
            item.setKey(key);
            item.setValue(memoryValue);
            return item;
        }

        @Override
        public ListMemoryEventsResponse listMemoryEvents(
                List<String> ns,
                List<String> kinds,
                OffsetDateTime after,
                OffsetDateTime before,
                String afterCursor,
                Integer limit) {
            throw new UnsupportedOperationException();
        }

        @Override
        public ListMemoryNamespacesResponse listMemoryNamespaces(
                List<String> prefix, List<String> suffix, Integer maxDepth, String archived) {
            throw new UnsupportedOperationException();
        }

        @Override
        public MemoryWriteResult putMemory(PutMemoryRequest putMemoryRequest) {
            lastPut = putMemoryRequest;
            return null;
        }

        @Override
        public SearchMemoriesResponse searchMemories(SearchMemoriesRequest searchMemoriesRequest) {
            throw new UnsupportedOperationException();
        }

        @Override
        public Response updateMemory(
                List<String> ns, String key, UpdateMemoryRequest updateMemoryRequest) {
            throw new UnsupportedOperationException();
        }
    }
}
