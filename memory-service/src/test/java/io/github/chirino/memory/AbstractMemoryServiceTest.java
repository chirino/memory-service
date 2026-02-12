package io.github.chirino.memory;

import static io.restassured.RestAssured.given;
import static org.hamcrest.MatcherAssert.assertThat;
import static org.hamcrest.Matchers.containsString;
import static org.hamcrest.Matchers.equalTo;
import static org.hamcrest.Matchers.greaterThan;
import static org.hamcrest.Matchers.hasSize;
import static org.hamcrest.Matchers.is;

import io.github.chirino.memory.api.dto.CreateConversationRequest;
import io.github.chirino.memory.api.dto.EntryDto;
import io.github.chirino.memory.api.dto.ShareConversationRequest;
import io.github.chirino.memory.client.model.CreateEntryRequest;
import io.github.chirino.memory.config.MemoryStoreSelector;
import io.github.chirino.memory.model.AccessLevel;
import io.github.chirino.memory.mongo.repo.MongoConversationMembershipRepository;
import io.github.chirino.memory.mongo.repo.MongoConversationOwnershipTransferRepository;
import io.github.chirino.memory.mongo.repo.MongoConversationRepository;
import io.github.chirino.memory.mongo.repo.MongoEntryRepository;
import io.github.chirino.memory.persistence.repo.ConversationMembershipRepository;
import io.github.chirino.memory.persistence.repo.ConversationOwnershipTransferRepository;
import io.github.chirino.memory.persistence.repo.ConversationRepository;
import io.github.chirino.memory.persistence.repo.EntryRepository;
import io.quarkus.test.security.TestSecurity;
import io.restassured.path.json.JsonPath;
import jakarta.enterprise.inject.Instance;
import jakarta.inject.Inject;
import jakarta.transaction.Transactional;
import jakarta.ws.rs.core.MediaType;
import java.time.OffsetDateTime;
import java.util.List;
import java.util.Map;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

/**
 * Backend-agnostic integration tests that exercise the Memory Service
 * through the HTTP API. Concrete subclasses provide the datastore
 * configuration (PostgreSQL or MongoDB).
 */
abstract class AbstractMemoryServiceTest {

    @Inject protected MemoryStoreSelector memoryStoreSelector;

    @Inject Instance<ConversationRepository> conversationRepository;
    @Inject Instance<EntryRepository> entryRepository;
    @Inject Instance<ConversationMembershipRepository> membershipRepository;
    @Inject Instance<ConversationOwnershipTransferRepository> ownershipTransferRepository;
    @Inject Instance<MongoConversationRepository> mongoConversationRepository;
    @Inject Instance<MongoEntryRepository> mongoEntryRepository;
    @Inject Instance<MongoConversationMembershipRepository> mongoMembershipRepository;
    @Inject Instance<MongoConversationOwnershipTransferRepository> mongoOwnershipTransferRepository;

    /**
     * Backend-specific hook used by the ownership transfer test to verify
     * that the transfer has been persisted correctly in the underlying store.
     */
    protected abstract void verifyOwnershipTransferPersisted(
            String conversationId, String fromUserId, String toUserId);

    @BeforeEach
    @Transactional
    void clearDatabase() {
        clearRelationalData();
        clearMongoData();
    }

    @Test
    @TestSecurity(user = "alice")
    void conversationLifecycle_userAndAgentMessages_andSearch() {
        // Create a conversation
        String conversationId =
                given().contentType(MediaType.APPLICATION_JSON)
                        .body(Map.of("title", "Test Conversation"))
                        .when()
                        .post("/v1/conversations")
                        .then()
                        .statusCode(201)
                        .body("title", is("Test Conversation"))
                        .body("ownerUserId", is("alice"))
                        .extract()
                        .path("id");

        // Conversation should appear in listing
        given().when()
                .get("/v1/conversations")
                .then()
                .statusCode(200)
                .body(
                        "data.find { it.id == '" + conversationId + "' }.title",
                        is("Test Conversation"));

        // // Users can no longer append messages via the HTTP API; it should return 403.
        // // Send a properly formatted CreateMessageRequest to pass validation, then get 403
        // given().contentType(MediaType.APPLICATION_JSON)
        //         .body(
        //                 Map.of(
        //                         "content",
        //                         List.of(Map.of("type", "text", "text", "Hello world from
        // Alice"))))
        //         .when()
        //         .post("/v1/conversations/{id}/messages", conversationId)
        //         .then()
        //         .statusCode(403)
        //         .body("code", is("forbidden"));

        // Seed a couple of user entries directly via the store
        memoryStoreSelector
                .getStore()
                .appendMemoryEntries(
                        "alice",
                        conversationId,
                        List.of(createHistoryEntry("Hello world from Alice")),
                        "test-client",
                        null);

        memoryStoreSelector
                .getStore()
                .appendMemoryEntries(
                        "alice",
                        conversationId,
                        List.of(createHistoryEntry("Second user entry with keyword alpha")),
                        "test-client",
                        null);

        // User-visible entries should be listed in order
        JsonPath entriesJson =
                given().when()
                        .get("/v1/conversations/{id}/entries", conversationId)
                        .then()
                        .statusCode(200)
                        .body("data", hasSize(2))
                        .extract()
                        .jsonPath();

        List<String> createdAtStrings = entriesJson.getList("data.createdAt", String.class);
        OffsetDateTime first = OffsetDateTime.parse(createdAtStrings.get(0));
        OffsetDateTime second = OffsetDateTime.parse(createdAtStrings.get(1));
        assertThat(second, greaterThan(first));

        // Append agent entries directly via the store so we can verify
        // the agent view without relying on a dedicated HTTP endpoint.
        io.github.chirino.memory.client.model.CreateEntryRequest a1 =
                new io.github.chirino.memory.client.model.CreateEntryRequest();
        a1.setChannel(io.github.chirino.memory.client.model.CreateEntryRequest.ChannelEnum.MEMORY);
        a1.setContent(List.of(Map.of("type", "text", "text", "Agent response one")));

        io.github.chirino.memory.client.model.CreateEntryRequest a2 =
                new io.github.chirino.memory.client.model.CreateEntryRequest();
        a2.setChannel(io.github.chirino.memory.client.model.CreateEntryRequest.ChannelEnum.MEMORY);
        a2.setContent(
                List.of(Map.of("type", "text", "text", "Agent response two with keyword beta")));

        memoryStoreSelector
                .getStore()
                .appendMemoryEntries("alice", conversationId, List.of(a1, a2), "test-agent", null);

        // Agent view of entries should include all entries
        given().header("X-API-Key", "test-agent-key")
                .when()
                .get("/v1/conversations/{id}/entries", conversationId)
                .then()
                .statusCode(200)
                .body("data.size()", equalTo(4));

        // Search should find entries by keyword
        Map<String, Object> searchRequest =
                Map.of("query", "alpha", "topK", 10, "conversationIds", List.of(conversationId));

        given().contentType(MediaType.APPLICATION_JSON)
                .body(searchRequest)
                .when()
                .post("/v1/conversations/search")
                .then()
                .statusCode(200)
                .body("data", hasSize(1))
                .body("data[0].entry.content[0].text", containsString("alpha"));
    }

    @Test
    @TestSecurity(user = "owner")
    void memberships_and_accessControl_enforced() {
        // Owner creates a conversation
        String conversationId =
                given().contentType(MediaType.APPLICATION_JSON)
                        .body(Map.of("title", "Membership Conversation"))
                        .when()
                        .post("/v1/conversations")
                        .then()
                        .statusCode(201)
                        .extract()
                        .path("id");

        // Owner shares with another user
        Map<String, Object> shareRequest =
                Map.of(
                        "userId", "writer",
                        "accessLevel", "WRITER");

        given().contentType(MediaType.APPLICATION_JSON)
                .body(shareRequest)
                .when()
                .post("/v1/conversations/{id}/memberships", conversationId)
                .then()
                .statusCode(201)
                .body("userId", is("writer"))
                .body("accessLevel", is("writer"));

        // Listing memberships shows both owner and shared user
        given().when()
                .get("/v1/conversations/{id}/memberships", conversationId)
                .then()
                .statusCode(200)
                .body("data.find { it.userId == 'owner' }.accessLevel", is("owner"))
                .body("data.find { it.userId == 'writer' }.accessLevel", is("writer"));
    }

    @Test
    @TestSecurity(user = "writer")
    void nonManager_cannot_manage_memberships() {
        // Prepare a conversation owned by "owner" with "writer" membership using the store directly
        CreateConversationRequest createConversationRequest = new CreateConversationRequest();
        createConversationRequest.setTitle("Access Control Conversation");
        String conversationId =
                memoryStoreSelector
                        .getStore()
                        .createConversation("owner", createConversationRequest)
                        .getId();

        ShareConversationRequest share = new ShareConversationRequest();
        share.setUserId("writer");
        share.setAccessLevel(AccessLevel.WRITER);
        memoryStoreSelector.getStore().shareConversation("owner", conversationId, share);

        // As "writer", attempting to share should be forbidden
        Map<String, Object> shareRequest =
                Map.of(
                        "userId", "another",
                        "accessLevel", "READER");

        given().contentType(MediaType.APPLICATION_JSON)
                .body(shareRequest)
                .when()
                .post("/v1/conversations/{id}/memberships", conversationId)
                .then()
                .statusCode(403)
                .body("code", is("forbidden"));
    }

    @Test
    @TestSecurity(user = "forker")
    void forking_creates_new_conversation_with_expected_history() {
        // Create base conversation with three user entries
        String conversationId =
                given().contentType(MediaType.APPLICATION_JSON)
                        .body(Map.of("title", "Fork Base"))
                        .when()
                        .post("/v1/conversations")
                        .then()
                        .statusCode(201)
                        .extract()
                        .path("id");

        List<EntryDto> r1 =
                memoryStoreSelector
                        .getStore()
                        .appendMemoryEntries(
                                "forker",
                                conversationId,
                                List.of(createHistoryEntry("Entry 1")),
                                "test-client",
                                null);
        EntryDto m1 = r1.get(0);

        List<EntryDto> r2 =
                memoryStoreSelector
                        .getStore()
                        .appendMemoryEntries(
                                "forker",
                                conversationId,
                                List.of(createHistoryEntry("Entry 2 - fork point")),
                                "test-client",
                                null);
        EntryDto m2 = r2.get(0);

        memoryStoreSelector
                .getStore()
                .appendMemoryEntries(
                        "forker",
                        conversationId,
                        List.of(createHistoryEntry("Entry 3")),
                        "test-client",
                        null);

        // Fork at the second entry by creating an entry on a new conversation with fork metadata
        String forkedId = java.util.UUID.randomUUID().toString();
        given().contentType(MediaType.APPLICATION_JSON)
                .header("X-API-Key", "test-agent-key")
                .body(
                        Map.of(
                                "channel",
                                "HISTORY",
                                "contentType",
                                "history",
                                "content",
                                List.of(Map.of("role", "USER", "text", "Fork message")),
                                "forkedAtConversationId",
                                conversationId,
                                "forkedAtEntryId",
                                m2.getId()))
                .when()
                .post("/v1/conversations/{id}/entries", forkedId)
                .then()
                .statusCode(201);

        // Verify fork conversation metadata
        given().when()
                .get("/v1/conversations/{id}", forkedId)
                .then()
                .statusCode(200)
                .body("forkedAtConversationId", is(conversationId))
                .body("forkedAtEntryId", is(m1.getId()));

        // Fork should include parent entries up to the fork point (Entry 1) plus the fork message
        given().when()
                .get("/v1/conversations/{id}/entries", forkedId)
                .then()
                .statusCode(200)
                .body("data", hasSize(2))
                .body("data[0].id", is(m1.getId()))
                .body("data[0].content[0].text", is("Entry 1"))
                .body("data[1].content[0].text", is("Fork message"));
    }

    @Test
    @TestSecurity(user = "owner-transfer")
    void ownershipTransfer_creates_transfer_record() {
        // Create conversation as the current user
        String conversationId =
                given().contentType(MediaType.APPLICATION_JSON)
                        .body(Map.of("title", "Ownership Conversation"))
                        .when()
                        .post("/v1/conversations")
                        .then()
                        .statusCode(201)
                        .extract()
                        .path("id");

        // Share conversation with new-owner (required before transfer)
        Map<String, Object> shareRequest =
                Map.of(
                        "userId", "new-owner",
                        "accessLevel", "MANAGER");

        given().contentType(MediaType.APPLICATION_JSON)
                .body(shareRequest)
                .when()
                .post("/v1/conversations/{id}/memberships", conversationId)
                .then()
                .statusCode(201);

        // Now create ownership transfer
        Map<String, Object> body =
                Map.of("conversationId", conversationId, "newOwnerUserId", "new-owner");

        given().contentType(MediaType.APPLICATION_JSON)
                .body(body)
                .when()
                .post("/v1/ownership-transfers")
                .then()
                .statusCode(201);

        verifyOwnershipTransferPersisted(conversationId, "owner-transfer", "new-owner");
    }

    private void clearRelationalData() {
        if (entryRepository.isUnsatisfied()) {
            return;
        }
        entryRepository.get().deleteAll();
        membershipRepository.get().deleteAll();
        ownershipTransferRepository.get().deleteAll();
        conversationRepository.get().deleteAll();
    }

    private void clearMongoData() {
        if (mongoEntryRepository.isUnsatisfied()) {
            return;
        }
        mongoEntryRepository.get().deleteAll();
        mongoMembershipRepository.get().deleteAll();
        mongoOwnershipTransferRepository.get().deleteAll();
        mongoConversationRepository.get().deleteAll();
    }

    protected static CreateEntryRequest createHistoryEntry(String text) {
        CreateEntryRequest req = new CreateEntryRequest();
        req.setChannel(CreateEntryRequest.ChannelEnum.HISTORY);
        req.setContentType("history");
        req.setContent(List.of(Map.of("role", "USER", "text", text)));
        return req;
    }
}
