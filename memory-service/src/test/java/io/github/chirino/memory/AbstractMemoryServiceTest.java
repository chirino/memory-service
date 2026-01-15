package io.github.chirino.memory;

import static io.restassured.RestAssured.given;
import static org.hamcrest.MatcherAssert.assertThat;
import static org.hamcrest.Matchers.containsString;
import static org.hamcrest.Matchers.equalTo;
import static org.hamcrest.Matchers.greaterThan;
import static org.hamcrest.Matchers.hasSize;
import static org.hamcrest.Matchers.is;

import io.github.chirino.memory.api.dto.CreateConversationRequest;
import io.github.chirino.memory.api.dto.CreateUserMessageRequest;
import io.github.chirino.memory.api.dto.MessageDto;
import io.github.chirino.memory.api.dto.ShareConversationRequest;
import io.github.chirino.memory.config.MemoryStoreSelector;
import io.github.chirino.memory.model.AccessLevel;
import io.github.chirino.memory.mongo.repo.MongoConversationMembershipRepository;
import io.github.chirino.memory.mongo.repo.MongoConversationOwnershipTransferRepository;
import io.github.chirino.memory.mongo.repo.MongoConversationRepository;
import io.github.chirino.memory.mongo.repo.MongoMessageRepository;
import io.github.chirino.memory.persistence.repo.ConversationMembershipRepository;
import io.github.chirino.memory.persistence.repo.ConversationOwnershipTransferRepository;
import io.github.chirino.memory.persistence.repo.ConversationRepository;
import io.github.chirino.memory.persistence.repo.MessageRepository;
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
    @Inject Instance<MessageRepository> messageRepository;
    @Inject Instance<ConversationMembershipRepository> membershipRepository;
    @Inject Instance<ConversationOwnershipTransferRepository> ownershipTransferRepository;
    @Inject Instance<MongoConversationRepository> mongoConversationRepository;
    @Inject Instance<MongoMessageRepository> mongoMessageRepository;
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

        // Users can no longer append messages via the HTTP API; it should return 403.
        // Send a properly formatted CreateMessageRequest to pass validation, then get 403
        given().contentType(MediaType.APPLICATION_JSON)
                .body(
                        Map.of(
                                "content",
                                List.of(Map.of("type", "text", "text", "Hello world from Alice"))))
                .when()
                .post("/v1/conversations/{id}/messages", conversationId)
                .then()
                .statusCode(403)
                .body("code", is("forbidden"));

        // Seed a couple of user messages directly via the store
        CreateUserMessageRequest u1 = new CreateUserMessageRequest();
        u1.setContent("Hello world from Alice");
        memoryStoreSelector.getStore().appendUserMessage("alice", conversationId, u1);

        CreateUserMessageRequest u2 = new CreateUserMessageRequest();
        u2.setContent("Second user message with keyword alpha");
        memoryStoreSelector.getStore().appendUserMessage("alice", conversationId, u2);

        // User-visible messages should be listed in order
        JsonPath messagesJson =
                given().when()
                        .get("/v1/conversations/{id}/messages", conversationId)
                        .then()
                        .statusCode(200)
                        .body("data", hasSize(2))
                        .extract()
                        .jsonPath();

        List<String> createdAtStrings = messagesJson.getList("data.createdAt", String.class);
        OffsetDateTime first = OffsetDateTime.parse(createdAtStrings.get(0));
        OffsetDateTime second = OffsetDateTime.parse(createdAtStrings.get(1));
        assertThat(second, greaterThan(first));

        // Append agent messages directly via the store so we can verify
        // the agent view without relying on a dedicated HTTP endpoint.
        io.github.chirino.memory.client.model.CreateMessageRequest a1 =
                new io.github.chirino.memory.client.model.CreateMessageRequest();
        a1.setChannel(
                io.github.chirino.memory.client.model.CreateMessageRequest.ChannelEnum.MEMORY);
        a1.setContent(List.of(Map.of("type", "text", "text", "Agent response one")));

        io.github.chirino.memory.client.model.CreateMessageRequest a2 =
                new io.github.chirino.memory.client.model.CreateMessageRequest();
        a2.setChannel(
                io.github.chirino.memory.client.model.CreateMessageRequest.ChannelEnum.MEMORY);
        a2.setContent(
                List.of(Map.of("type", "text", "text", "Agent response two with keyword beta")));

        memoryStoreSelector
                .getStore()
                .appendAgentMessages("alice", conversationId, List.of(a1, a2), "test-agent");

        // Agent view of messages should include all messages
        given().header("X-API-Key", "test-agent-key")
                .when()
                .get("/v1/conversations/{id}/messages", conversationId)
                .then()
                .statusCode(200)
                .body("data.size()", equalTo(4));

        // Search should find messages by keyword
        Map<String, Object> searchRequest =
                Map.of("query", "alpha", "topK", 10, "conversationIds", List.of(conversationId));

        given().contentType(MediaType.APPLICATION_JSON)
                .body(searchRequest)
                .when()
                .post("/v1/user/search/messages")
                .then()
                .statusCode(200)
                .body("data", hasSize(1))
                .body("data[0].message.content[0].text", containsString("alpha"));
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
                .post("/v1/conversations/{id}/forks", conversationId)
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
                .post("/v1/conversations/{id}/forks", conversationId)
                .then()
                .statusCode(403)
                .body("code", is("forbidden"));
    }

    @Test
    @TestSecurity(user = "forker")
    void forking_creates_new_conversation_with_expected_history() {
        // Create base conversation with three user messages
        String conversationId =
                given().contentType(MediaType.APPLICATION_JSON)
                        .body(Map.of("title", "Fork Base"))
                        .when()
                        .post("/v1/conversations")
                        .then()
                        .statusCode(201)
                        .extract()
                        .path("id");

        CreateUserMessageRequest u1 = new CreateUserMessageRequest();
        u1.setContent("Message 1");
        MessageDto m1 =
                memoryStoreSelector.getStore().appendUserMessage("forker", conversationId, u1);

        CreateUserMessageRequest u2 = new CreateUserMessageRequest();
        u2.setContent("Message 2 - fork point");
        MessageDto m2 =
                memoryStoreSelector.getStore().appendUserMessage("forker", conversationId, u2);

        CreateUserMessageRequest u3 = new CreateUserMessageRequest();
        u3.setContent("Message 3");
        memoryStoreSelector.getStore().appendUserMessage("forker", conversationId, u3);

        // Fork at the second message
        String forkedId =
                given().contentType(MediaType.APPLICATION_JSON)
                        .body(Map.of("title", "Forked Conversation"))
                        .when()
                        .post(
                                "/v1/conversations/{cid}/messages/{mid}/fork",
                                conversationId,
                                m2.getId())
                        .then()
                        .statusCode(201)
                        .body("title", is("Forked Conversation"))
                        .body("forkedAtConversationId", is(conversationId))
                        .body("forkedAtMessageId", is(m1.getId()))
                        .extract()
                        .path("id");

        // Fork should start empty (no message copying)
        given().when()
                .get("/v1/conversations/{id}/messages", forkedId)
                .then()
                .statusCode(200)
                .body("data", hasSize(0));
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

        Map<String, Object> body = Map.of("newOwnerUserId", "new-owner");

        given().contentType(MediaType.APPLICATION_JSON)
                .body(body)
                .when()
                .post("/v1/conversations/{id}/transfer-ownership", conversationId)
                .then()
                .statusCode(202);

        verifyOwnershipTransferPersisted(conversationId, "owner-transfer", "new-owner");
    }

    private void clearRelationalData() {
        if (messageRepository.isUnsatisfied()) {
            return;
        }
        messageRepository.get().deleteAll();
        membershipRepository.get().deleteAll();
        ownershipTransferRepository.get().deleteAll();
        conversationRepository.get().deleteAll();
    }

    private void clearMongoData() {
        if (mongoMessageRepository.isUnsatisfied()) {
            return;
        }
        mongoMessageRepository.get().deleteAll();
        mongoMembershipRepository.get().deleteAll();
        mongoOwnershipTransferRepository.get().deleteAll();
        mongoConversationRepository.get().deleteAll();
    }
}
