package io.github.chirino.memory.cucumber;

import static io.restassured.RestAssured.given;
import static org.hamcrest.MatcherAssert.assertThat;
import static org.hamcrest.Matchers.containsString;
import static org.hamcrest.Matchers.greaterThan;
import static org.hamcrest.Matchers.hasSize;
import static org.hamcrest.Matchers.is;
import static org.hamcrest.Matchers.notNullValue;
import static org.hamcrest.Matchers.nullValue;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.github.difflib.DiffUtils;
import com.github.difflib.UnifiedDiffUtils;
import com.github.difflib.patch.Patch;
import com.google.protobuf.Empty;
import com.google.protobuf.Message;
import com.google.protobuf.TextFormat;
import com.google.protobuf.util.JsonFormat;
import com.jayway.jsonpath.InvalidPathException;
import io.github.chirino.memory.api.dto.CreateConversationRequest;
import io.github.chirino.memory.api.dto.CreateUserMessageRequest;
import io.github.chirino.memory.client.model.CreateMessageRequest;
import io.github.chirino.memory.config.MemoryStoreSelector;
import io.github.chirino.memory.grpc.v1.AppendMessageRequest;
import io.github.chirino.memory.grpc.v1.CheckConversationsRequest;
import io.github.chirino.memory.grpc.v1.Conversation;
import io.github.chirino.memory.grpc.v1.ConversationMembership;
import io.github.chirino.memory.grpc.v1.ConversationMembershipsServiceGrpc;
import io.github.chirino.memory.grpc.v1.ConversationsServiceGrpc;
import io.github.chirino.memory.grpc.v1.CreateSummaryRequest;
import io.github.chirino.memory.grpc.v1.DeleteConversationRequest;
import io.github.chirino.memory.grpc.v1.DeleteMembershipRequest;
import io.github.chirino.memory.grpc.v1.ForkConversationRequest;
import io.github.chirino.memory.grpc.v1.GetConversationRequest;
import io.github.chirino.memory.grpc.v1.HasResponseInProgressRequest;
import io.github.chirino.memory.grpc.v1.HealthResponse;
import io.github.chirino.memory.grpc.v1.ListConversationsRequest;
import io.github.chirino.memory.grpc.v1.ListConversationsResponse;
import io.github.chirino.memory.grpc.v1.ListForksRequest;
import io.github.chirino.memory.grpc.v1.ListForksResponse;
import io.github.chirino.memory.grpc.v1.ListMembershipsRequest;
import io.github.chirino.memory.grpc.v1.ListMembershipsResponse;
import io.github.chirino.memory.grpc.v1.ListMessagesRequest;
import io.github.chirino.memory.grpc.v1.ListMessagesResponse;
import io.github.chirino.memory.grpc.v1.MessagesServiceGrpc;
import io.github.chirino.memory.grpc.v1.MutinyResponseResumerServiceGrpc;
import io.github.chirino.memory.grpc.v1.ReplayResponseTokensRequest;
import io.github.chirino.memory.grpc.v1.ReplayResponseTokensResponse;
import io.github.chirino.memory.grpc.v1.ResponseResumerServiceGrpc;
import io.github.chirino.memory.grpc.v1.SearchMessagesRequest;
import io.github.chirino.memory.grpc.v1.SearchMessagesResponse;
import io.github.chirino.memory.grpc.v1.SearchServiceGrpc;
import io.github.chirino.memory.grpc.v1.ShareConversationRequest;
import io.github.chirino.memory.grpc.v1.StreamResponseTokenRequest;
import io.github.chirino.memory.grpc.v1.StreamResponseTokenResponse;
import io.github.chirino.memory.grpc.v1.SyncMessagesRequest;
import io.github.chirino.memory.grpc.v1.SystemServiceGrpc;
import io.github.chirino.memory.grpc.v1.TransferOwnershipRequest;
import io.github.chirino.memory.grpc.v1.UpdateMembershipRequest;
import io.github.chirino.memory.mongo.repo.MongoConversationMembershipRepository;
import io.github.chirino.memory.mongo.repo.MongoConversationOwnershipTransferRepository;
import io.github.chirino.memory.mongo.repo.MongoConversationRepository;
import io.github.chirino.memory.mongo.repo.MongoMessageRepository;
import io.github.chirino.memory.persistence.repo.ConversationMembershipRepository;
import io.github.chirino.memory.persistence.repo.ConversationOwnershipTransferRepository;
import io.github.chirino.memory.persistence.repo.ConversationRepository;
import io.github.chirino.memory.persistence.repo.MessageRepository;
import io.github.chirino.memory.store.AccessDeniedException;
import io.github.chirino.memory.store.ResourceNotFoundException;
import io.grpc.ManagedChannel;
import io.grpc.ManagedChannelBuilder;
import io.grpc.Metadata;
import io.grpc.Status;
import io.grpc.StatusRuntimeException;
import io.grpc.stub.MetadataUtils;
import io.quarkiverse.cucumber.ScenarioScope;
import io.quarkus.test.keycloak.client.KeycloakTestClient;
import io.restassured.path.json.JsonPath;
import io.restassured.response.Response;
import jakarta.enterprise.inject.Instance;
import jakarta.inject.Inject;
import jakarta.transaction.Transactional;
import jakarta.ws.rs.core.MediaType;
import java.net.URI;
import java.net.URISyntaxException;
import java.util.Arrays;
import java.util.HashMap;
import java.util.Iterator;
import java.util.List;
import java.util.Map;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.regex.Matcher;
import java.util.regex.Pattern;
import org.eclipse.microprofile.config.Config;
import org.eclipse.microprofile.config.ConfigProvider;

@ScenarioScope
public class StepDefinitions {

    private static final ObjectMapper OBJECT_MAPPER = new ObjectMapper();
    private static final Pattern PLACEHOLDER_PATTERN = Pattern.compile("\\$\\{([^}]+)}");
    private static final Metadata.Key<String> AUTHORIZATION_METADATA =
            Metadata.Key.of("Authorization", Metadata.ASCII_STRING_MARSHALLER);
    private static final Metadata.Key<String> API_KEY_METADATA =
            Metadata.Key.of("X-API-Key", Metadata.ASCII_STRING_MARSHALLER);

    @Inject MemoryStoreSelector memoryStoreSelector;

    @Inject Instance<ConversationRepository> conversationRepository;
    @Inject Instance<MessageRepository> messageRepository;
    @Inject Instance<ConversationMembershipRepository> membershipRepository;
    @Inject Instance<ConversationOwnershipTransferRepository> ownershipTransferRepository;
    @Inject Instance<MongoConversationRepository> mongoConversationRepository;
    @Inject Instance<MongoMessageRepository> mongoMessageRepository;
    @Inject Instance<MongoConversationMembershipRepository> mongoMembershipRepository;
    @Inject Instance<MongoConversationOwnershipTransferRepository> mongoOwnershipTransferRepository;

    private final KeycloakTestClient keycloakClient = new KeycloakTestClient();

    private String currentUserId;
    private String currentApiKey;
    private String conversationId;
    private Response lastResponse;
    private final Map<String, Object> contextVariables = new HashMap<>();
    private ManagedChannel grpcChannel;
    private String lastGrpcResponseJson;
    private JsonPath lastGrpcJsonPath;
    private Throwable lastGrpcError;
    private Message lastGrpcMessage;
    private String lastGrpcServiceMethod;
    private String lastGrpcResponseText;
    private CompletableFuture<StreamResponseTokenResponse> inProgressStreamResponse;
    private CountDownLatch streamStartedLatch;
    private AtomicInteger streamedTokenCount;
    private String replayedTokens;
    private boolean replayFinishedBeforeStreamComplete;

    @io.cucumber.java.Before(order = 0)
    public void setupGrpcChannel() {
        if (grpcChannel != null) {
            return;
        }
        GrpcEndpoint endpoint = resolveGrpcEndpoint();
        grpcChannel =
                ManagedChannelBuilder.forAddress(endpoint.host(), endpoint.port())
                        .usePlaintext()
                        .build();
    }

    @io.cucumber.java.After
    public void tearDownGrpcChannel() {
        if (grpcChannel != null) {
            grpcChannel.shutdownNow();
            grpcChannel = null;
        }
    }

    @io.cucumber.java.Before
    @Transactional
    public void clearDatabase() {
        clearRelationalData();
        clearMongoData();
    }

    @io.cucumber.java.en.Given("I am authenticated as user {string}")
    public void iAmAuthenticatedAsUser(String userId) {
        this.currentUserId = userId;
        this.currentApiKey = null;
        // We'll use KeycloakTestClient to get a real token when making requests
    }

    @io.cucumber.java.en.Given("I am authenticated as agent with API key {string}")
    public void iAmAuthenticatedAsAgentWithApiKey(String apiKey) {
        this.currentUserId = "alice"; // Default user for agent context
        this.currentApiKey = apiKey;
    }

    @io.cucumber.java.en.Given("I have a conversation with title {string}")
    public void iHaveAConversationWithTitle(String title) {
        CreateConversationRequest request = new CreateConversationRequest();
        request.setTitle(title);
        this.conversationId =
                memoryStoreSelector.getStore().createConversation(currentUserId, request).getId();
        contextVariables.put("conversationId", conversationId);
        contextVariables.put("conversationOwner", currentUserId);
    }

    @io.cucumber.java.en.Given("the conversation exists")
    public void theConversationExists() {
        if (this.conversationId == null) {
            iHaveAConversationWithTitle("Test Conversation");
        }
    }

    @io.cucumber.java.en.Given("the conversation id is {string}")
    public void theConversationIdIs(String id) {
        this.conversationId = id;
        try {
            memoryStoreSelector.getStore().deleteConversation(currentUserId, id);
        } catch (ResourceNotFoundException | AccessDeniedException ignored) {
            // Ignored; we only need to ensure the id is not already in use by the current user.
        }
        contextVariables.put("conversationId", conversationId);
    }

    @io.cucumber.java.en.Given("the conversation has no messages")
    public void theConversationHasNoMessages() {
        // Conversation already exists from background, no action needed
    }

    @io.cucumber.java.en.Given("the conversation has a message {string}")
    public void theConversationHasAMessage(String content) {
        CreateUserMessageRequest request = new CreateUserMessageRequest();
        request.setContent(content);
        memoryStoreSelector.getStore().appendUserMessage(currentUserId, conversationId, request);
    }

    @io.cucumber.java.en.Given("the conversation has {int} messages")
    public void theConversationHasMessages(int count) {
        for (int i = 1; i <= count; i++) {
            theConversationHasAMessage("Message " + i);
        }
    }

    @io.cucumber.java.en.Given("the conversation has a message {string} in channel {string}")
    public void theConversationHasAMessageInChannel(String content, String channel) {
        CreateMessageRequest request = new CreateMessageRequest();
        request.setContent(List.of(Map.of("type", "text", "text", content)));
        request.setChannel(CreateMessageRequest.ChannelEnum.fromString(channel.toLowerCase()));
        memoryStoreSelector
                .getStore()
                .appendAgentMessages(currentUserId, conversationId, List.of(request));
    }

    @io.cucumber.java.en.Given("the conversation has a memory message {string} with epoch {int}")
    public void theConversationHasAMemoryMessageWithEpoch(String content, int epoch) {
        CreateMessageRequest request = new CreateMessageRequest();
        request.setContent(List.of(Map.of("type", "text", "text", content)));
        request.setChannel(CreateMessageRequest.ChannelEnum.MEMORY);
        request.setMemoryEpoch((long) epoch);
        memoryStoreSelector
                .getStore()
                .appendAgentMessages(currentUserId, conversationId, List.of(request));
    }

    @io.cucumber.java.en.Given("I have streamed tokens {string} to the conversation")
    public void iHaveStreamedTokensToTheConversation(String tokens) throws Exception {
        // Stream tokens character by character to simulate token streaming
        Metadata metadata = buildGrpcMetadata();
        var mutinyStub = MutinyResponseResumerServiceGrpc.newMutinyStub(grpcChannel);
        if (metadata != null) {
            mutinyStub =
                    mutinyStub.withInterceptors(
                            MetadataUtils.newAttachHeadersInterceptor(metadata));
        }

        // Create a stream of token requests
        var tokenRequests = new java.util.ArrayList<StreamResponseTokenRequest>();
        for (int i = 0; i < tokens.length(); i++) {
            String token = String.valueOf(tokens.charAt(i));
            var requestBuilder = StreamResponseTokenRequest.newBuilder();
            if (i == 0) {
                requestBuilder.setConversationId(conversationId);
            }
            requestBuilder.setToken(token);
            requestBuilder.setComplete(i == tokens.length() - 1);
            tokenRequests.add(requestBuilder.build());
        }

        // Create a Multi stream that will complete after all items are emitted
        var requestStream = io.smallrye.mutiny.Multi.createFrom().iterable(tokenRequests);

        // Await the response with a timeout to prevent hanging
        try {
            mutinyStub
                    .streamResponseTokens(requestStream)
                    .await()
                    .atMost(java.time.Duration.ofSeconds(10));
        } catch (Exception e) {
            throw new AssertionError("Failed to stream response tokens: " + e.getMessage(), e);
        }
    }

    @io.cucumber.java.en.Given(
            "I start streaming tokens {string} to the conversation with {int}ms delay and keep the"
                    + " stream open for {int}ms")
    public void iStartStreamingTokensToTheConversationWithDelay(
            String tokens, int delayMs, int holdOpenMs) throws Exception {
        Metadata metadata = buildGrpcMetadata();
        var mutinyStub = MutinyResponseResumerServiceGrpc.newMutinyStub(grpcChannel);
        if (metadata != null) {
            mutinyStub =
                    mutinyStub.withInterceptors(
                            MetadataUtils.newAttachHeadersInterceptor(metadata));
        }

        streamStartedLatch = new CountDownLatch(1);
        streamedTokenCount = new AtomicInteger(0);
        inProgressStreamResponse = new CompletableFuture<>();

        var requestStream =
                io.smallrye.mutiny.Multi.createFrom()
                        .<StreamResponseTokenRequest>emitter(
                                emitter -> {
                                    Thread thread =
                                            new Thread(
                                                    () -> {
                                                        try {
                                                            for (int i = 0;
                                                                    i < tokens.length();
                                                                    i++) {
                                                                String token =
                                                                        String.valueOf(
                                                                                tokens.charAt(i));
                                                                var requestBuilder =
                                                                        StreamResponseTokenRequest
                                                                                .newBuilder();
                                                                if (i == 0) {
                                                                    requestBuilder
                                                                            .setConversationId(
                                                                                    conversationId);
                                                                }
                                                                requestBuilder.setToken(token);
                                                                requestBuilder.setComplete(false);
                                                                emitter.emit(
                                                                        requestBuilder.build());
                                                                if (streamedTokenCount
                                                                                .incrementAndGet()
                                                                        == 1) {
                                                                    streamStartedLatch.countDown();
                                                                }
                                                                if (delayMs > 0) {
                                                                    Thread.sleep(delayMs);
                                                                }
                                                            }
                                                            if (holdOpenMs > 0) {
                                                                Thread.sleep(holdOpenMs);
                                                            }
                                                            emitter.emit(
                                                                    StreamResponseTokenRequest
                                                                            .newBuilder()
                                                                            .setComplete(true)
                                                                            .build());
                                                            emitter.complete();
                                                        } catch (InterruptedException e) {
                                                            Thread.currentThread().interrupt();
                                                            emitter.fail(e);
                                                        } catch (Exception e) {
                                                            emitter.fail(e);
                                                        }
                                                    },
                                                    "response-resumer-stream");
                                    thread.setDaemon(true);
                                    thread.start();
                                });

        mutinyStub
                .streamResponseTokens(requestStream)
                .subscribe()
                .with(
                        inProgressStreamResponse::complete,
                        inProgressStreamResponse::completeExceptionally);
    }

    @io.cucumber.java.en.Given("I wait for the response stream to send at least {int} tokens")
    public void iWaitForTheResponseStreamToSendAtLeastTokens(int count)
            throws InterruptedException {
        if (streamStartedLatch == null || streamedTokenCount == null) {
            throw new AssertionError("No response stream is in progress");
        }
        if (!streamStartedLatch.await(5, TimeUnit.SECONDS)) {
            throw new AssertionError("Timed out waiting for response stream to start");
        }
        long deadline = System.nanoTime() + TimeUnit.SECONDS.toNanos(5);
        while (streamedTokenCount.get() < count && System.nanoTime() < deadline) {
            Thread.sleep(10);
        }
        if (streamedTokenCount.get() < count) {
            throw new AssertionError(
                    "Timed out waiting for at least " + count + " streamed tokens");
        }
    }

    @io.cucumber.java.en.When(
            "I replay response tokens from position {long} in a second session and collect tokens"
                    + " {string}")
    public void iReplayResponseTokensFromPositionInSecondSessionAndCollectTokens(
            long resumePosition, String expectedTokens) {
        if (inProgressStreamResponse == null) {
            throw new AssertionError("No in-progress response stream found");
        }

        Metadata metadata = buildGrpcMetadata();
        var mutinyStub = MutinyResponseResumerServiceGrpc.newMutinyStub(grpcChannel);
        if (metadata != null) {
            mutinyStub =
                    mutinyStub.withInterceptors(
                            MetadataUtils.newAttachHeadersInterceptor(metadata));
        }

        var request =
                ReplayResponseTokensRequest.newBuilder()
                        .setConversationId(conversationId)
                        .setResumePosition(resumePosition)
                        .build();

        int expectedCount = expectedTokens.length();
        List<ReplayResponseTokensResponse> responses;
        try {
            responses =
                    mutinyStub
                            .replayResponseTokens(request)
                            .select()
                            .first(expectedCount)
                            .collect()
                            .asList()
                            .await()
                            .atMost(java.time.Duration.ofSeconds(10));
        } catch (Exception e) {
            throw new AssertionError(
                    "Failed while replaying response tokens: " + e.getMessage(), e);
        }

        StringBuilder received = new StringBuilder();
        for (ReplayResponseTokensResponse response : responses) {
            received.append(response.getToken());
        }

        replayedTokens = received.toString();
        if (!expectedTokens.equals(replayedTokens)) {
            throw new AssertionError(
                    "Expected replayed tokens to be \""
                            + expectedTokens
                            + "\" but was \""
                            + replayedTokens
                            + "\"");
        }
        replayFinishedBeforeStreamComplete = !inProgressStreamResponse.isDone();
    }

    @io.cucumber.java.en.Then("the replay should finish before the stream completes")
    public void theReplayShouldFinishBeforeTheStreamCompletes() {
        if (!replayFinishedBeforeStreamComplete) {
            throw new AssertionError("Replay finished after the stream completed");
        }
    }

    @io.cucumber.java.en.Then("I wait for the response stream to complete")
    public void iWaitForTheResponseStreamToComplete() {
        if (inProgressStreamResponse == null) {
            throw new AssertionError("No response stream is in progress");
        }
        try {
            inProgressStreamResponse.get(10, TimeUnit.SECONDS);
        } catch (Exception e) {
            throw new AssertionError("Timed out waiting for response stream completion", e);
        }
    }

    @io.cucumber.java.en.Given("there is a conversation owned by {string}")
    public void thereIsAConversationOwnedBy(String ownerId) {
        CreateConversationRequest request = new CreateConversationRequest();
        request.setTitle("Owned by " + ownerId);
        this.conversationId =
                memoryStoreSelector.getStore().createConversation(ownerId, request).getId();
        contextVariables.put("conversationId", conversationId);
        contextVariables.put("conversationOwner", ownerId);
    }

    @io.cucumber.java.en.When("I list messages for the conversation")
    public void iListMessagesForTheConversation() {
        iListMessagesForTheConversationWithParams(null, null, null, null);
    }

    @io.cucumber.java.en.When("I list messages with limit {int}")
    public void iListMessagesWithLimit(int limit) {
        iListMessagesForTheConversationWithParams(null, limit, null, null);
    }

    @io.cucumber.java.en.When("I list messages for the conversation with channel {string}")
    public void iListMessagesForTheConversationWithChannel(String channel) {
        iListMessagesForTheConversationWithParams(null, null, channel, null);
    }

    @io.cucumber.java.en.When("I list memory messages for the conversation with epoch {string}")
    public void iListMemoryMessagesForTheConversationWithEpoch(String epoch) {
        iListMessagesForTheConversationWithParams(null, null, "MEMORY", epoch);
    }

    @io.cucumber.java.en.When("I list messages for conversation {string}")
    public void iListMessagesForConversation(String convId) {
        this.conversationId = convId;
        iListMessagesForTheConversation();
    }

    @io.cucumber.java.en.When("I list messages for that conversation")
    public void iListMessagesForThatConversation() {
        iListMessagesForTheConversation();
    }

    private void iListMessagesForTheConversationWithParams(
            String after, Integer limit, String channel, String epoch) {
        var requestSpec = given();
        // Add Authorization header using KeycloakTestClient to get a real token
        // Agents need both OIDC auth AND API key
        if (currentUserId != null) {
            String token = keycloakClient.getAccessToken(currentUserId);
            requestSpec = requestSpec.auth().oauth2(token);
        }
        if (currentApiKey != null) {
            requestSpec = requestSpec.header("X-API-Key", currentApiKey);
        }
        var request = requestSpec.when();
        if (after != null) {
            request = request.queryParam("after", after);
        }
        if (limit != null) {
            request = request.queryParam("limit", limit);
        }
        if (channel != null) {
            request = request.queryParam("channel", channel);
        }
        if (epoch != null) {
            request = request.queryParam("epoch", epoch);
        }
        this.lastResponse = request.get("/v1/conversations/{id}/messages", conversationId);
    }

    @io.cucumber.java.en.When("I append a message with content {string} and channel {string}")
    public void iAppendAMessageWithContentAndChannel(String content, String channel) {
        var requestSpec =
                given().contentType(MediaType.APPLICATION_JSON)
                        .body(
                                Map.of(
                                        "content",
                                        List.of(Map.of("type", "text", "text", content)),
                                        "channel",
                                        channel));
        // Add Authorization header using KeycloakTestClient
        // Agents need both OIDC auth AND API key
        if (currentUserId != null) {
            String token = keycloakClient.getAccessToken(currentUserId);
            requestSpec = requestSpec.auth().oauth2(token);
        }
        if (currentApiKey != null) {
            requestSpec = requestSpec.header("X-API-Key", currentApiKey);
        }
        this.lastResponse =
                requestSpec.when().post("/v1/conversations/{id}/messages", conversationId);
    }

    @io.cucumber.java.en.When("I try to append a message with content {string}")
    public void iTryToAppendAMessageWithContent(String content) {
        var requestSpec =
                given().contentType(MediaType.APPLICATION_JSON)
                        .body(Map.of("content", List.of(Map.of("type", "text", "text", content))));
        // Add Authorization header using KeycloakTestClient
        if (currentUserId != null) {
            String token = keycloakClient.getAccessToken(currentUserId);
            requestSpec = requestSpec.auth().oauth2(token);
        }
        if (currentApiKey != null) {
            requestSpec = requestSpec.header("X-API-Key", currentApiKey);
        }
        this.lastResponse =
                requestSpec.when().post("/v1/conversations/{id}/messages", conversationId);
    }

    @io.cucumber.java.en.When("I sync memory messages with request:")
    public void iSyncMemoryMessagesWithRequest(String requestBody) {
        String rendered = renderTemplate(requestBody);
        var requestSpec = given().contentType(MediaType.APPLICATION_JSON).body(rendered);
        // Add Authorization header using KeycloakTestClient
        // Agents need both OIDC auth AND API key
        if (currentUserId != null) {
            String token = keycloakClient.getAccessToken(currentUserId);
            requestSpec = requestSpec.auth().oauth2(token);
        }
        if (currentApiKey != null) {
            requestSpec = requestSpec.header("X-API-Key", currentApiKey);
        }
        this.lastResponse =
                requestSpec
                        .when()
                        .post("/v1/conversations/{id}/memory/messages/sync", conversationId);
    }

    @io.cucumber.java.en.Then("the sync response should contain {int} messages")
    public void theSyncResponseShouldContainMessages(int count) {
        if (lastResponse == null) {
            throw new AssertionError("No response has been received");
        }
        JsonPath jsonPath = lastResponse.jsonPath();
        List<Object> messages = jsonPath.getList("messages");
        if (messages == null) {
            throw new AssertionError(
                    "Response does not contain 'messages' field. Response body: "
                            + lastResponse.getBody().asString());
        }
        assertThat(
                "Sync response should contain " + count + " messages", messages.size(), is(count));
    }

    @io.cucumber.java.en.When(
            "I create a summary with title {string} and summary {string} and untilMessageId"
                    + " {string} and summarizedAt {string}")
    public void iCreateASummary(
            String title, String summary, String untilMessageId, String summarizedAt) {
        var requestSpec =
                given().contentType(MediaType.APPLICATION_JSON)
                        .body(
                                Map.of(
                                        "title",
                                        title,
                                        "summary",
                                        summary,
                                        "untilMessageId",
                                        untilMessageId,
                                        "summarizedAt",
                                        summarizedAt));
        if (currentUserId != null) {
            String token = keycloakClient.getAccessToken(currentUserId);
            requestSpec = requestSpec.auth().oauth2(token);
        }
        if (currentApiKey != null) {
            requestSpec = requestSpec.header("X-API-Key", currentApiKey);
        }
        this.lastResponse =
                requestSpec.when().post("/v1/conversations/{id}/summaries", conversationId);
    }

    @io.cucumber.java.en.When("I create a summary with request:")
    public void iCreateASummaryWithRequest(String requestBody) {
        String rendered = renderTemplate(requestBody);
        var requestSpec = given().contentType(MediaType.APPLICATION_JSON).body(rendered);
        if (currentUserId != null) {
            String token = keycloakClient.getAccessToken(currentUserId);
            requestSpec = requestSpec.auth().oauth2(token);
        }
        if (currentApiKey != null) {
            requestSpec = requestSpec.header("X-API-Key", currentApiKey);
        }
        this.lastResponse =
                requestSpec.when().post("/v1/conversations/{id}/summaries", conversationId);
    }

    @io.cucumber.java.en.When("I search messages with request:")
    public void iSearchMessagesWithRequest(String requestBody) {
        String rendered = renderTemplate(requestBody);
        var requestSpec = given().contentType(MediaType.APPLICATION_JSON).body(rendered);
        if (currentUserId != null) {
            String token = keycloakClient.getAccessToken(currentUserId);
            requestSpec = requestSpec.auth().oauth2(token);
        }
        if (currentApiKey != null) {
            requestSpec = requestSpec.header("X-API-Key", currentApiKey);
        }
        this.lastResponse = requestSpec.when().post("/v1/user/search/messages");
    }

    @io.cucumber.java.en.Then("the search response should contain at least {int} results")
    public void theSearchResponseShouldContainAtLeastResults(int minCount) {
        lastResponse.then().body("data.size()", greaterThan(minCount - 1));
    }

    @io.cucumber.java.en.When("I search messages for query {string}")
    public void iSearchMessagesForQuery(String query) {
        var requestSpec =
                given().contentType(MediaType.APPLICATION_JSON).body(Map.of("query", query));
        if (currentUserId != null) {
            String token = keycloakClient.getAccessToken(currentUserId);
            requestSpec = requestSpec.auth().oauth2(token);
        }
        if (currentApiKey != null) {
            requestSpec = requestSpec.header("X-API-Key", currentApiKey);
        }
        this.lastResponse = requestSpec.when().post("/v1/user/search/messages");
    }

    @io.cucumber.java.en.When("I create a conversation with request:")
    public void iCreateAConversationWithRequest(String requestBody) {
        String rendered = renderTemplate(requestBody);
        var requestSpec = given().contentType(MediaType.APPLICATION_JSON).body(rendered);
        if (currentUserId != null) {
            String token = keycloakClient.getAccessToken(currentUserId);
            requestSpec = requestSpec.auth().oauth2(token);
        }
        this.lastResponse = requestSpec.when().post("/v1/conversations");
        if (lastResponse.getStatusCode() == 201) {
            String id = lastResponse.jsonPath().getString("id");
            if (id != null) {
                this.conversationId = id;
                contextVariables.put("conversationId", id);
                if (currentUserId != null) {
                    contextVariables.put("conversationOwner", currentUserId);
                }
            }
        }
    }

    @io.cucumber.java.en.When("I list conversations")
    public void iListConversations() {
        iListConversationsWithParams(null, null, null);
    }

    @io.cucumber.java.en.When("I list conversations with limit {int}")
    public void iListConversationsWithLimit(int limit) {
        iListConversationsWithParams(null, limit, null);
    }

    @io.cucumber.java.en.When("I list conversations with limit {int} and after {string}")
    public void iListConversationsWithLimitAndAfter(int limit, String after) {
        iListConversationsWithParams(after, limit, null);
    }

    @io.cucumber.java.en.When("I list conversations with query {string}")
    public void iListConversationsWithQuery(String query) {
        iListConversationsWithParams(null, null, query);
    }

    private void iListConversationsWithParams(String after, Integer limit, String query) {
        var requestSpec = given();
        if (currentUserId != null) {
            String token = keycloakClient.getAccessToken(currentUserId);
            requestSpec = requestSpec.auth().oauth2(token);
        }
        var request = requestSpec.when();
        if (after != null) {
            request = request.queryParam("after", after);
        }
        if (limit != null) {
            request = request.queryParam("limit", limit);
        }
        if (query != null) {
            request = request.queryParam("query", query);
        }
        this.lastResponse = request.get("/v1/conversations");
    }

    @io.cucumber.java.en.When("I get the conversation")
    public void iGetTheConversation() {
        iGetConversation(conversationId);
    }

    @io.cucumber.java.en.When("I get conversation {string}")
    public void iGetConversation(String convId) {
        String renderedConvId = renderTemplate(convId);
        // Strip quotes if present (RestAssured path parameters shouldn't have quotes)
        if (renderedConvId.startsWith("\"") && renderedConvId.endsWith("\"")) {
            renderedConvId = renderedConvId.substring(1, renderedConvId.length() - 1);
        }
        var requestSpec = given();
        if (currentUserId != null) {
            String token = keycloakClient.getAccessToken(currentUserId);
            if (token != null) {
                requestSpec = requestSpec.auth().oauth2(token);
            }
        }
        this.lastResponse = requestSpec.when().get("/v1/conversations/{id}", renderedConvId);
    }

    @io.cucumber.java.en.When("I get that conversation")
    public void iGetThatConversation() {
        iGetTheConversation();
    }

    @io.cucumber.java.en.When("I delete the conversation")
    public void iDeleteTheConversation() {
        iDeleteConversation(conversationId);
    }

    @io.cucumber.java.en.When("I delete conversation {string}")
    public void iDeleteConversation(String convId) {
        var requestSpec = given();
        if (currentUserId != null) {
            String token = keycloakClient.getAccessToken(currentUserId);
            requestSpec = requestSpec.auth().oauth2(token);
        }
        this.lastResponse = requestSpec.when().delete("/v1/conversations/{id}", convId);
    }

    @io.cucumber.java.en.When("I delete that conversation")
    public void iDeleteThatConversation() {
        iDeleteTheConversation();
    }

    @io.cucumber.java.en.When("I transfer ownership of the conversation to {string} with request:")
    public void iTransferOwnershipOfTheConversationToWithRequest(
            String newOwner, String requestBody) {
        String rendered = renderTemplate(requestBody);
        var requestSpec = given().contentType(MediaType.APPLICATION_JSON).body(rendered);
        if (currentUserId != null) {
            String token = keycloakClient.getAccessToken(currentUserId);
            requestSpec = requestSpec.auth().oauth2(token);
        }
        this.lastResponse =
                requestSpec
                        .when()
                        .post("/v1/conversations/{id}/transfer-ownership", conversationId);
    }

    @io.cucumber.java.en.When(
            "I transfer ownership of conversation {string} to {string} with request:")
    public void iTransferOwnershipOfConversationToWithRequest(
            String convId, String newOwner, String requestBody) {
        String rendered = renderTemplate(requestBody);
        var requestSpec = given().contentType(MediaType.APPLICATION_JSON).body(rendered);
        if (currentUserId != null) {
            String token = keycloakClient.getAccessToken(currentUserId);
            requestSpec = requestSpec.auth().oauth2(token);
        }
        this.lastResponse =
                requestSpec.when().post("/v1/conversations/{id}/transfer-ownership", convId);
    }

    @io.cucumber.java.en.When("I transfer ownership of that conversation to {string} with request:")
    public void iTransferOwnershipOfThatConversationToWithRequest(
            String newOwner, String requestBody) {
        iTransferOwnershipOfTheConversationToWithRequest(newOwner, requestBody);
    }

    @io.cucumber.java.en.Then("the response should contain at least {int} conversations")
    public void theResponseShouldContainAtLeastConversations(int minCount) {
        lastResponse.then().body("data.size()", greaterThan(minCount - 1));
    }

    @io.cucumber.java.en.Then("the response should contain at least {int} conversation")
    public void theResponseShouldContainAtLeastConversation(int minCount) {
        theResponseShouldContainAtLeastConversations(minCount);
    }

    @io.cucumber.java.en.Then("the response should contain at least {int} memberships")
    public void theResponseShouldContainAtLeastMemberships(int minCount) {
        lastResponse.then().body("data.size()", greaterThan(minCount - 1));
    }

    @io.cucumber.java.en.Then("the response should contain {int} conversations")
    public void theResponseShouldContainConversations(int count) {
        lastResponse.then().body("data", hasSize(count));
    }

    @io.cucumber.java.en.Then("the response body should contain {string}")
    public void theResponseBodyShouldContain(String text) {
        String body = lastResponse.getBody().asString();
        assertThat("Response body should contain: " + text, body, containsString(text));
    }

    @io.cucumber.java.en.When("I fork the conversation at message {string} with request:")
    public void iForkTheConversationAtMessageWithRequest(String messageId, String requestBody) {
        String rendered = renderTemplate(requestBody);
        var requestSpec = given().contentType(MediaType.APPLICATION_JSON).body(rendered);
        if (currentUserId != null) {
            String token = keycloakClient.getAccessToken(currentUserId);
            requestSpec = requestSpec.auth().oauth2(token);
        }
        String renderedMessageId = renderTemplate(messageId);
        // Remove quotes if present (from template rendering)
        if (renderedMessageId.startsWith("\"") && renderedMessageId.endsWith("\"")) {
            renderedMessageId = renderedMessageId.substring(1, renderedMessageId.length() - 1);
        }
        this.lastResponse =
                requestSpec
                        .when()
                        .post(
                                "/v1/conversations/{id}/messages/{mid}/fork",
                                conversationId,
                                renderedMessageId);
        if (lastResponse.getStatusCode() == 201) {
            String id = lastResponse.jsonPath().getString("id");
            if (id != null) {
                contextVariables.put("forkedConversationId", id);
            }
        }
    }

    @io.cucumber.java.en.When("I fork conversation {string} at message {string} with request:")
    public void iForkConversationAtMessageWithRequest(
            String convId, String messageId, String requestBody) {
        String rendered = renderTemplate(requestBody);
        var requestSpec = given().contentType(MediaType.APPLICATION_JSON).body(rendered);
        if (currentUserId != null) {
            String token = keycloakClient.getAccessToken(currentUserId);
            requestSpec = requestSpec.auth().oauth2(token);
        }
        String renderedMessageId = renderTemplate(messageId);
        // Remove quotes if present (from template rendering)
        if (renderedMessageId.startsWith("\"") && renderedMessageId.endsWith("\"")) {
            renderedMessageId = renderedMessageId.substring(1, renderedMessageId.length() - 1);
        }
        this.lastResponse =
                requestSpec
                        .when()
                        .post(
                                "/v1/conversations/{id}/messages/{mid}/fork",
                                convId,
                                renderedMessageId);
    }

    @io.cucumber.java.en.When("I fork that conversation at message {string} with request:")
    public void iForkThatConversationAtMessageWithRequest(String messageId, String requestBody) {
        iForkTheConversationAtMessageWithRequest(messageId, requestBody);
    }

    @io.cucumber.java.en.When("I list forks for the conversation")
    public void iListForksForTheConversation() {
        iListForksForConversation(conversationId);
    }

    @io.cucumber.java.en.When("I list forks for conversation {string}")
    public void iListForksForConversation(String convId) {
        var requestSpec = given();
        if (currentUserId != null) {
            String token = keycloakClient.getAccessToken(currentUserId);
            requestSpec = requestSpec.auth().oauth2(token);
        }
        this.lastResponse = requestSpec.when().get("/v1/conversations/{id}/forks", convId);
    }

    @io.cucumber.java.en.When("I list forks for that conversation")
    public void iListForksForThatConversation() {
        iListForksForTheConversation();
    }

    @io.cucumber.java.en.Then("the response should contain at least {int} forks")
    public void theResponseShouldContainAtLeastForks(int minCount) {
        lastResponse.then().body("data.size()", greaterThan(minCount - 1));
    }

    @io.cucumber.java.en.When("I share the conversation with user {string} with request:")
    public void iShareTheConversationWithUserWithRequest(String userId, String requestBody) {
        String rendered = renderTemplate(requestBody);
        var requestSpec = given().contentType(MediaType.APPLICATION_JSON).body(rendered);
        if (currentUserId != null) {
            String token = keycloakClient.getAccessToken(currentUserId);
            requestSpec = requestSpec.auth().oauth2(token);
        }
        this.lastResponse = requestSpec.when().post("/v1/conversations/{id}/forks", conversationId);
    }

    @io.cucumber.java.en.When("I share conversation {string} with user {string} with request:")
    public void iShareConversationWithUserWithRequest(
            String convId, String userId, String requestBody) {
        String rendered = renderTemplate(requestBody);
        var requestSpec = given().contentType(MediaType.APPLICATION_JSON).body(rendered);
        if (currentUserId != null) {
            String token = keycloakClient.getAccessToken(currentUserId);
            requestSpec = requestSpec.auth().oauth2(token);
        }
        this.lastResponse = requestSpec.when().post("/v1/conversations/{id}/forks", convId);
    }

    @io.cucumber.java.en.When("I share that conversation with user {string} with request:")
    public void iShareThatConversationWithUserWithRequest(String userId, String requestBody) {
        iShareTheConversationWithUserWithRequest(userId, requestBody);
    }

    @io.cucumber.java.en.When("I list memberships for the conversation")
    public void iListMembershipsForTheConversation() {
        iListMembershipsForConversation(conversationId);
    }

    @io.cucumber.java.en.When("I list memberships for conversation {string}")
    public void iListMembershipsForConversation(String convId) {
        var requestSpec = given();
        if (currentUserId != null) {
            String token = keycloakClient.getAccessToken(currentUserId);
            if (token == null) {
                throw new AssertionError(
                        "Failed to get access token for user: "
                                + currentUserId
                                + ". KeycloakTestClient may not be properly configured.");
            }
            requestSpec = requestSpec.auth().oauth2(token);
        }
        this.lastResponse = requestSpec.when().get("/v1/conversations/{id}/memberships", convId);
    }

    @io.cucumber.java.en.When("I list memberships for that conversation")
    public void iListMembershipsForThatConversation() {
        iListMembershipsForTheConversation();
    }

    @io.cucumber.java.en.When("I update membership for user {string} with request:")
    public void iUpdateMembershipForUserWithRequest(String userId, String requestBody) {
        String rendered = renderTemplate(requestBody);
        var requestSpec = given().contentType(MediaType.APPLICATION_JSON).body(rendered);
        if (currentUserId != null) {
            String token = keycloakClient.getAccessToken(currentUserId);
            if (token == null) {
                throw new AssertionError(
                        "Failed to get access token for user: "
                                + currentUserId
                                + ". KeycloakTestClient may not be properly configured.");
            }
            requestSpec = requestSpec.auth().oauth2(token);
        }
        this.lastResponse =
                requestSpec
                        .when()
                        .patch("/v1/conversations/{id}/memberships/{uid}", conversationId, userId);
    }

    @io.cucumber.java.en.When("I delete membership for user {string}")
    public void iDeleteMembershipForUser(String userId) {
        var requestSpec = given();
        if (currentUserId != null) {
            String token = keycloakClient.getAccessToken(currentUserId);
            if (token == null) {
                throw new AssertionError(
                        "Failed to get access token for user: "
                                + currentUserId
                                + ". KeycloakTestClient may not be properly configured.");
            }
            requestSpec = requestSpec.auth().oauth2(token);
        }
        this.lastResponse =
                requestSpec
                        .when()
                        .delete("/v1/conversations/{id}/memberships/{uid}", conversationId, userId);
    }

    @io.cucumber.java.en.Given(
            "the conversation is shared with user {string} with access level {string}")
    @Transactional
    public void theConversationIsSharedWithUserWithAccessLevel(String userId, String accessLevel) {
        // Use the owner of the conversation to share it, not the current user
        // First try to get owner from context (set when conversation was created)
        String ownerId = (String) contextVariables.get("conversationOwner");

        // If not in context, get from repository (bypassing access checks)
        if (ownerId == null) {
            if (conversationRepository.isResolvable() && conversationRepository.get() != null) {
                // PostgreSQL
                var entity =
                        conversationRepository
                                .get()
                                .findByIdOptional(java.util.UUID.fromString(conversationId))
                                .orElseThrow(
                                        () ->
                                                new RuntimeException(
                                                        "Conversation not found: "
                                                                + conversationId));
                ownerId = entity.getOwnerUserId();
            } else if (mongoConversationRepository.isResolvable()
                    && mongoConversationRepository.get() != null) {
                // MongoDB
                var entity = mongoConversationRepository.get().findById(conversationId);
                if (entity == null) {
                    throw new RuntimeException("Conversation not found: " + conversationId);
                }
                ownerId = entity.ownerUserId;
            } else {
                throw new RuntimeException(
                        "Cannot determine conversation owner. conversationId: " + conversationId);
            }
        }

        io.github.chirino.memory.api.dto.ShareConversationRequest request =
                new io.github.chirino.memory.api.dto.ShareConversationRequest();
        request.setUserId(userId);
        request.setAccessLevel(
                io.github.chirino.memory.model.AccessLevel.fromString(accessLevel.toLowerCase()));

        // Use the owner to share the conversation (they have manager access)
        memoryStoreSelector.getStore().shareConversation(ownerId, conversationId, request);
    }

    @io.cucumber.java.en.Then(
            "the response should contain a membership for user {string} with access level {string}")
    public void theResponseShouldContainAMembershipForUserWithAccessLevel(
            String userId, String accessLevel) {
        JsonPath jsonPath = lastResponse.jsonPath();
        List<Map<String, Object>> memberships = jsonPath.getList("data");
        boolean found =
                memberships.stream()
                        .anyMatch(
                                m ->
                                        userId.equals(m.get("userId"))
                                                && accessLevel.equalsIgnoreCase(
                                                        String.valueOf(m.get("accessLevel"))));
        assertThat(
                "Should contain membership for user "
                        + userId
                        + " with access level "
                        + accessLevel,
                found,
                is(true));
    }

    @io.cucumber.java.en.Then("the response should not contain a membership for user {string}")
    public void theResponseShouldNotContainAMembershipForUser(String userId) {
        JsonPath jsonPath = lastResponse.jsonPath();
        List<Map<String, Object>> memberships = jsonPath.getList("data");
        boolean found = memberships.stream().anyMatch(m -> userId.equals(m.get("userId")));
        assertThat("Should not contain membership for user " + userId, found, is(false));
    }

    @io.cucumber.java.en.Then("the response should contain {int} membership")
    public void theResponseShouldContainMembership(int count) {
        lastResponse.then().body("data", hasSize(count));
    }

    @io.cucumber.java.en.When("I send gRPC request {string} with body:")
    public void iSendGrpcRequestWithBody(String serviceMethod, String body) {
        if (grpcChannel == null) {
            throw new IllegalStateException("gRPC channel is not initialized");
        }
        lastGrpcResponseJson = null;
        lastGrpcJsonPath = null;
        lastGrpcError = null;
        try {
            String renderedBody = renderTemplate(body);
            Message response = callGrpcService(serviceMethod, renderedBody);
            StringBuilder textBuilder = new StringBuilder();
            TextFormat.printer().print(response, textBuilder);
            lastGrpcResponseText = textBuilder.toString();
            lastGrpcResponseJson =
                    JsonFormat.printer().includingDefaultValueFields().print(response);
            lastGrpcJsonPath = JsonPath.from(lastGrpcResponseJson);
            lastGrpcMessage = response;
            lastGrpcServiceMethod = serviceMethod;
        } catch (StatusRuntimeException e) {
            lastGrpcError = e;
        } catch (Exception e) {
            throw new AssertionError("Failed to invoke gRPC method " + serviceMethod, e);
        }
    }

    @io.cucumber.java.en.Then("the response status should be {int}")
    public void theResponseStatusShouldBe(int statusCode) {
        lastResponse.then().statusCode(statusCode);
    }

    @io.cucumber.java.en.Then("the response body {string} should be {string}")
    public void theResponseBodyFieldShouldBe(String path, String expected) {
        String renderedExpected = renderTemplate(expected);
        // Handle null values
        if ("null".equals(renderedExpected)) {
            lastResponse.then().body(path, nullValue());
        } else {
            // Remove quotes if the rendered expected is a quoted string
            if (renderedExpected.startsWith("\"") && renderedExpected.endsWith("\"")) {
                renderedExpected = renderedExpected.substring(1, renderedExpected.length() - 1);
            }
            // Handle boolean values - convert "true"/"false" strings to boolean for comparison
            if ("true".equalsIgnoreCase(renderedExpected)
                    || "false".equalsIgnoreCase(renderedExpected)) {
                boolean expectedBool = Boolean.parseBoolean(renderedExpected);
                lastResponse.then().body(path, is(expectedBool));
            } else {
                // Try to parse as number for numeric comparisons
                try {
                    if (renderedExpected.matches("-?\\d+")) {
                        // It's an integer - use number matcher that accepts both int and long
                        long expectedLong = Long.parseLong(renderedExpected);
                        // Use a matcher that accepts both Integer and Long
                        lastResponse
                                .then()
                                .body(
                                        path,
                                        org.hamcrest.Matchers.anyOf(
                                                is((int) expectedLong), is(expectedLong)));
                    } else if (renderedExpected.matches("-?\\d+\\.\\d+")) {
                        // It's a floating point number
                        double expectedDouble = Double.parseDouble(renderedExpected);
                        lastResponse.then().body(path, is(expectedDouble));
                    } else {
                        // It's a string
                        lastResponse.then().body(path, is(renderedExpected));
                    }
                } catch (NumberFormatException e) {
                    // Not a number, treat as string
                    lastResponse.then().body(path, is(renderedExpected));
                }
            }
        }
    }

    @io.cucumber.java.en.Then("the response should contain an empty list of messages")
    public void theResponseShouldContainAnEmptyListOfMessages() {
        lastResponse.then().body("data", hasSize(0));
    }

    @io.cucumber.java.en.Then("the response should contain {int} messages")
    public void theResponseShouldContainMessages(int count) {
        lastResponse.then().body("data", hasSize(count));
    }

    @io.cucumber.java.en.Then("the response should contain {int} message")
    public void theResponseShouldContainMessage(int count) {
        lastResponse.then().body("data", hasSize(count));
    }

    @io.cucumber.java.en.Then("the response body field {string} should be {string}")
    public void theResponseBodyFieldShouldBe2(String path, String expected) {
        // Alias for "the response body {string} should be {string}" to match test feature file
        theResponseBodyFieldShouldBe(path, expected);
    }

    @io.cucumber.java.en.Then("the search response should contain {int} results")
    public void theSearchResponseShouldContainResults(int count) {
        lastResponse.then().body("data", hasSize(count));
    }

    @io.cucumber.java.en.Then("search result at index {int} should have message content {string}")
    public void searchResultAtIndexShouldHaveMessageContent(int index, String expectedContent) {
        JsonPath jsonPath = lastResponse.jsonPath();
        String actualContent = jsonPath.getString("data[" + index + "].message.content[0].text");
        assertThat(actualContent, is(expectedContent));
    }

    @io.cucumber.java.en.Then("message at index {int} should have content {string}")
    public void messageAtIndexShouldHaveContent(int index, String expectedContent) {
        JsonPath jsonPath = lastResponse.jsonPath();
        String actualContent = jsonPath.getString("data[" + index + "].content[0].text");
        assertThat(actualContent, is(expectedContent));
    }

    @io.cucumber.java.en.Then("the response should have a nextCursor")
    public void theResponseShouldHaveANextCursor() {
        lastResponse.then().body("nextCursor", notNullValue());
    }

    @io.cucumber.java.en.Then("the response should contain the created message")
    public void theResponseShouldContainTheCreatedMessage() {
        lastResponse.then().body("id", notNullValue());
    }

    @io.cucumber.java.en.Then("the message should have content {string}")
    public void theMessageShouldHaveContent(String expectedContent) {
        lastResponse.then().body("content[0].text", is(expectedContent));
    }

    @io.cucumber.java.en.Then("the message should have channel {string}")
    public void theMessageShouldHaveChannel(String expectedChannel) {
        lastResponse.then().body("channel", is(expectedChannel.toLowerCase()));
    }

    @io.cucumber.java.en.Then("the response should contain error code {string}")
    public void theResponseShouldContainErrorCode(String errorCode) {
        lastResponse.then().body("code", is(errorCode));
    }

    @io.cucumber.java.en.Then("set {string} to the json response field {string}")
    public void setContextVariableToJsonResponseField(String variableName, String path) {
        if (lastResponse == null) {
            throw new AssertionError("No HTTP response has been received");
        }
        JsonPath jsonPath = lastResponse.jsonPath();
        Object value = jsonPath.get(path);
        if (value == null) {
            throw new AssertionError(
                    "JSON response field '" + path + "' is null or does not exist");
        }
        contextVariables.put(variableName, value);
    }

    @io.cucumber.java.en.Then("the gRPC response should contain {int} message")
    public void theGrpcResponseShouldContainMessage(int count) {
        assertGrpcMessageCount(count);
    }

    @io.cucumber.java.en.Then("the gRPC response should contain {int} messages")
    public void theGrpcResponseShouldContainMessages(int count) {
        assertGrpcMessageCount(count);
    }

    @io.cucumber.java.en.Then("gRPC message at index {int} should have content {string}")
    public void grpcMessageAtIndexShouldHaveContent(int index, String expectedContent) {
        JsonPath jsonPath = ensureGrpcJsonPath();
        String actualContent = jsonPath.getString("data[" + index + "].content[0].text");
        assertThat(actualContent, is(expectedContent));
    }

    @io.cucumber.java.en.Then("the gRPC response field {string} should be {string}")
    public void theGrpcResponseFieldShouldBe(String path, String expected) {
        JsonPath jsonPath = ensureGrpcJsonPath();
        String renderedExpected = renderTemplate(expected);
        // Remove quotes if the rendered expected is a quoted string
        if (renderedExpected.startsWith("\"") && renderedExpected.endsWith("\"")) {
            renderedExpected = renderedExpected.substring(1, renderedExpected.length() - 1);
        }
        Object value = jsonPath.get(path);
        if (value == null) {
            throw new AssertionError(
                    "gRPC response field '"
                            + path
                            + "' is null or does not exist. Available JSON: "
                            + lastGrpcResponseJson);
        }
        String actual = String.valueOf(value);
        assertThat(actual, is(renderedExpected));
    }

    @io.cucumber.java.en.Then("the gRPC response field {string} should not be null")
    public void theGrpcResponseFieldShouldNotBeNull(String path) {
        JsonPath jsonPath = ensureGrpcJsonPath();
        Object value = jsonPath.get(path);
        assertThat("gRPC response field '" + path + "' should not be null", value, notNullValue());
    }

    @io.cucumber.java.en.Then("set {string} to the gRPC response field {string}")
    public void setContextVariableToGrpcResponseField(String variableName, String path) {
        JsonPath jsonPath = ensureGrpcJsonPath();
        Object value = jsonPath.get(path);
        if (value == null) {
            throw new AssertionError(
                    "gRPC response field '" + path + "' is null or does not exist");
        }
        contextVariables.put(variableName, value);
    }

    @io.cucumber.java.en.Then("the gRPC response text should contain:")
    public void theGrpcResponseTextShouldContain(String expectedText) {
        if (lastGrpcResponseText == null) {
            throw new AssertionError("No gRPC response has been captured yet");
        }
        String rendered = renderTemplate(expectedText).trim();
        String actual = lastGrpcResponseText.trim();
        if (!actual.contains(rendered)) {
            throw new AssertionError(
                    "Expected gRPC response text to contain:\n"
                            + rendered
                            + "\nActual response text:\n"
                            + actual);
        }
    }

    @io.cucumber.java.en.Then("the gRPC response text should match text proto:")
    public void theGrpcResponseTextShouldMatchTextProto(String expectedText) {
        if (lastGrpcResponseJson == null) {
            throw new AssertionError("No gRPC response has been captured yet");
        }
        if (lastGrpcServiceMethod == null) {
            throw new AssertionError("No gRPC method has been invoked yet");
        }
        Message.Builder expectedBuilder = createGrpcResponseBuilder(lastGrpcServiceMethod);
        try {
            String rendered = renderTemplate(expectedText).trim();
            TextFormat.merge(rendered, expectedBuilder);
        } catch (TextFormat.ParseException e) {
            throw new AssertionError(
                    "Failed to parse expected gRPC text proto: " + e.getMessage(), e);
        }
        Message expectedMessage = expectedBuilder.build();
        try {
            // Don't include default values for expected - only check fields explicitly set in text
            // proto
            JsonNode expectedNode =
                    OBJECT_MAPPER.readTree(JsonFormat.printer().print(expectedMessage));
            JsonNode actualNode = OBJECT_MAPPER.readTree(lastGrpcResponseJson);
            assertJsonNodeContains(actualNode, expectedNode, "$");
        } catch (com.fasterxml.jackson.core.JsonProcessingException
                | com.google.protobuf.InvalidProtocolBufferException e) {
            throw new AssertionError(
                    "Failed to parse gRPC JSON representation: " + e.getMessage(), e);
        }
    }

    @io.cucumber.java.en.Then("the gRPC response should have status {string}")
    public void theGrpcResponseShouldHaveStatus(String expectedStatus) {
        if (lastGrpcError == null) {
            throw new AssertionError(
                    "Expected gRPC error with status " + expectedStatus + " but no error occurred");
        }
        if (!(lastGrpcError instanceof StatusRuntimeException)) {
            throw new AssertionError(
                    "Expected StatusRuntimeException but got: " + lastGrpcError.getClass());
        }
        StatusRuntimeException sre = (StatusRuntimeException) lastGrpcError;
        Status.Code expectedCode = Status.Code.valueOf(expectedStatus);
        assertThat("gRPC status code", sre.getStatus().getCode(), is(expectedCode));
    }

    @io.cucumber.java.en.Then("the gRPC response should not have an error")
    public void theGrpcResponseShouldNotHaveAnError() {
        if (lastGrpcError != null) {
            throw new AssertionError(
                    "Expected no gRPC error but got: " + lastGrpcError.getMessage(), lastGrpcError);
        }
        if (lastGrpcResponseJson == null) {
            throw new AssertionError("No gRPC response has been captured yet");
        }
    }

    @io.cucumber.java.en.Then("the gRPC response field {string} should be true")
    public void theGrpcResponseFieldShouldBeTrue(String fieldPath) {
        JsonPath jsonPath = ensureGrpcJsonPath();
        Boolean value = jsonPath.get(fieldPath);
        assertThat("gRPC response field " + fieldPath, value, is(true));
    }

    @io.cucumber.java.en.Then("the gRPC response field {string} should be false")
    public void theGrpcResponseFieldShouldBeFalse(String fieldPath) {
        JsonPath jsonPath = ensureGrpcJsonPath();
        Boolean value = jsonPath.get(fieldPath);
        assertThat("gRPC response field " + fieldPath, value, is(false));
    }

    @io.cucumber.java.en.Then("the gRPC response field {string} should be {int}")
    public void theGrpcResponseFieldShouldBe(String fieldPath, Integer expectedValue) {
        JsonPath jsonPath = ensureGrpcJsonPath();
        Object value = jsonPath.get(fieldPath);
        // Handle both String and Integer types (JSON path might return String)
        Integer actualValue;
        if (value instanceof String) {
            actualValue = Integer.valueOf((String) value);
        } else if (value instanceof Integer) {
            actualValue = (Integer) value;
        } else if (value instanceof Number) {
            actualValue = ((Number) value).intValue();
        } else {
            throw new AssertionError(
                    "Expected integer value for field " + fieldPath + " but got: " + value);
        }
        assertThat("gRPC response field " + fieldPath, actualValue, is(expectedValue));
    }

    @io.cucumber.java.en.Then("the conversation title should be {string}")
    public void theConversationTitleShouldBe(String expectedTitle) {
        var dto = memoryStoreSelector.getStore().getConversation(currentUserId, conversationId);
        assertThat(dto.getTitle(), is(expectedTitle));
    }

    @io.cucumber.java.en.Given("I set context variable {string} to {string}")
    public void iSetContextVariableTo(String name, String value) {
        // Resolve template variables in the value before storing
        String resolvedValue = renderTemplate(value);
        // Remove surrounding quotes if present (from JSON serialization)
        if (resolvedValue != null
                && resolvedValue.length() >= 2
                && resolvedValue.startsWith("\"")
                && resolvedValue.endsWith("\"")) {
            resolvedValue = resolvedValue.substring(1, resolvedValue.length() - 1);
        }
        contextVariables.put(name, resolvedValue);
    }

    @io.cucumber.java.en.Given("I set context variable {string} to json:")
    public void iSetContextVariableToJson(String name, String jsonValue) {
        try {
            Object parsed = OBJECT_MAPPER.readValue(jsonValue, Object.class);
            contextVariables.put(name, parsed);
        } catch (com.fasterxml.jackson.core.JsonProcessingException e) {
            throw new AssertionError(
                    "Failed to parse JSON for context variable " + name + ": " + e.getMessage(), e);
        }
    }

    @io.cucumber.java.en.Then("the response body should be json:")
    public void theResponseBodyShouldBeJson(String expectedJson) {
        // Parse both JSONs
        JsonNode actualNode = null, expectedNode = null;
        String expectedPretty = null, actualPretty = null;

        if (expectedJson != null && !expectedJson.isBlank()) {
            try {
                String rendered = renderTemplate(expectedJson);
                expectedNode = OBJECT_MAPPER.readTree(rendered);
                expectedPretty =
                        OBJECT_MAPPER
                                .writerWithDefaultPrettyPrinter()
                                .writeValueAsString(expectedNode);
            } catch (com.fasterxml.jackson.core.JsonProcessingException e) {
                throw new AssertionError(
                        "Failed to parse expected JSON: "
                                + e.getMessage()
                                + "\nJSON:\n"
                                + expectedJson,
                        e);
            }
        }

        var actualJson = lastResponse.getBody().asString();
        try {
            actualNode = OBJECT_MAPPER.readTree(actualJson);
            actualPretty =
                    OBJECT_MAPPER.writerWithDefaultPrettyPrinter().writeValueAsString(actualNode);
        } catch (com.fasterxml.jackson.core.JsonProcessingException e) {
            throw new AssertionError(
                    "Failed to parse actual JSON: " + e.getMessage() + "\nJSON:\n" + actualJson, e);
        }

        // Compare semantically (ignoring field order)
        if (actualNode.equals(expectedNode)) {
            return;
        }

        // Build error message with diff
        StringBuilder errorMessage = new StringBuilder();
        errorMessage.append("JSON response body does not match expected:\n\n");

        if (expectedPretty == null) {
            errorMessage.append("No expected JSON provided. Actual JSON:\n");
            errorMessage.append(actualPretty);
        } else {

            // Generate unified diff
            List<String> expectedLines = Arrays.asList(expectedPretty.split("\n"));
            List<String> actualLines = Arrays.asList(actualPretty.split("\n"));

            Patch<String> patch = DiffUtils.diff(expectedLines, actualLines);
            List<String> unifiedDiff =
                    UnifiedDiffUtils.generateUnifiedDiff(
                            "expected.json", "actual.json", expectedLines, patch, 3);

            errorMessage.append("Unified Diff:\n");
            unifiedDiff.forEach(line -> errorMessage.append(line).append("\n"));
        }
        throw new AssertionError(errorMessage.toString());
    }

    private String renderTemplate(String template) {
        if (template == null || template.isBlank()) {
            return template;
        }

        JsonPath responseJson = lastResponse != null ? lastResponse.jsonPath() : null;
        JsonPath grpcJsonPath = lastGrpcJsonPath;
        JsonPath contextJson = JsonPath.from(serializeContextVariables());

        Matcher matcher = PLACEHOLDER_PATTERN.matcher(template);
        StringBuilder result = new StringBuilder();
        int lastIndex = 0;
        while (matcher.find()) {
            result.append(template, lastIndex, matcher.start());
            String expression = matcher.group(1).trim();
            Object value = resolveExpression(expression, responseJson, grpcJsonPath, contextJson);
            boolean inQuotes = isSurroundedByQuotes(template, matcher.start(), matcher.end());
            result.append(serializeReplacement(value, inQuotes));
            lastIndex = matcher.end();
        }
        result.append(template.substring(lastIndex));
        return result.toString();
    }

    private Object resolveExpression(
            String expression, JsonPath responseJson, JsonPath grpcJsonPath, JsonPath contextJson) {
        try {
            if (expression.equals("response.body")) {
                if (responseJson != null) {
                    return responseJson.get("$");
                }
                if (grpcJsonPath != null) {
                    return grpcJsonPath.get("$");
                }
                throw new AssertionError(
                        "Cannot evaluate '"
                                + expression
                                + "' because no HTTP or gRPC response is available in the current"
                                + " scenario");
            }
            if (expression.startsWith("response.body.")) {
                String path = expression.substring("response.body.".length());
                if (responseJson != null) {
                    return responseJson.get(path);
                }
                if (grpcJsonPath != null) {
                    return grpcJsonPath.get(path);
                }
                throw new AssertionError(
                        "Cannot evaluate '"
                                + expression
                                + "' because no HTTP or gRPC response is available in the current"
                                + " scenario");
            }
            if (expression.equals("context")) {
                return contextVariables;
            }
            if (expression.startsWith("context.")) {
                String path = expression.substring("context.".length());
                return contextJson.get(path);
            }
            if (contextVariables.containsKey(expression)) {
                return contextVariables.get(expression);
            }
        } catch (InvalidPathException e) {
            throw new AssertionError(
                    "Invalid expression path '" + expression + "': " + e.getMessage(), e);
        }
        throw new AssertionError(
                "Unknown expression '"
                        + expression
                        + "'. Supported: response.body[.*], context[.*]");
    }

    private void ensureHttpResponseAvailable(String expression, JsonPath responseJson) {
        if (responseJson == null) {
            throw new AssertionError(
                    "Cannot evaluate '"
                            + expression
                            + "' because no HTTP response is available in the current scenario");
        }
    }

    private boolean isSurroundedByQuotes(String template, int start, int end) {
        int before = start - 1;
        int after = end;
        return before >= 0
                && after < template.length()
                && template.charAt(before) == '"'
                && template.charAt(after) == '"';
    }

    private String serializeReplacement(Object value, boolean inQuotes) {
        try {
            String json = OBJECT_MAPPER.writeValueAsString(value);
            if (inQuotes && json.length() >= 2 && json.startsWith("\"") && json.endsWith("\"")) {
                return json.substring(1, json.length() - 1);
            }
            return json;
        } catch (com.fasterxml.jackson.core.JsonProcessingException e) {
            throw new AssertionError("Failed to serialize placeholder value: " + e.getMessage(), e);
        }
    }

    private String serializeContextVariables() {
        try {
            return OBJECT_MAPPER.writeValueAsString(contextVariables);
        } catch (com.fasterxml.jackson.core.JsonProcessingException e) {
            throw new AssertionError("Failed to serialize context variables: " + e.getMessage(), e);
        }
    }

    private void assertGrpcMessageCount(int count) {
        JsonPath jsonPath = ensureGrpcJsonPath();
        assertThat(jsonPath.getList("messages"), hasSize(count));
    }

    private Message.Builder createGrpcResponseBuilder(String serviceMethod) {
        return switch (serviceMethod) {
            case "MessagesService/ListMessages" -> ListMessagesResponse.newBuilder();
            case "MessagesService/AppendMessage" ->
                    io.github.chirino.memory.grpc.v1.Message.newBuilder();
            case "SearchService/CreateSummary" ->
                    io.github.chirino.memory.grpc.v1.Message.newBuilder();
            case "SearchService/SearchMessages" -> SearchMessagesResponse.newBuilder();
            case "SystemService/GetHealth" -> HealthResponse.newBuilder();
            case "ConversationsService/ListConversations" -> ListConversationsResponse.newBuilder();
            case "ConversationsService/CreateConversation" -> Conversation.newBuilder();
            case "ConversationsService/GetConversation" -> Conversation.newBuilder();
            case "ConversationsService/DeleteConversation" -> Empty.newBuilder();
            case "ConversationsService/ForkConversation" -> Conversation.newBuilder();
            case "ConversationsService/ListForks" -> ListForksResponse.newBuilder();
            case "ConversationsService/TransferOwnership" -> Empty.newBuilder();
            case "ConversationMembershipsService/ListMemberships" ->
                    ListMembershipsResponse.newBuilder();
            case "ConversationMembershipsService/ShareConversation" ->
                    ConversationMembership.newBuilder();
            case "ConversationMembershipsService/UpdateMembership" ->
                    ConversationMembership.newBuilder();
            case "ConversationMembershipsService/DeleteMembership" -> Empty.newBuilder();
            default ->
                    throw new IllegalArgumentException(
                            "Unsupported gRPC method for text comparison: " + serviceMethod);
        };
    }

    private void assertJsonNodeContains(JsonNode actual, JsonNode expected, String path) {
        if (expected.isObject()) {
            Iterator<String> fieldNames = expected.fieldNames();
            while (fieldNames.hasNext()) {
                String fieldName = fieldNames.next();
                if (!actual.has(fieldName)) {
                    throw new AssertionError(
                            "Expected field '"
                                    + path
                                    + "."
                                    + fieldName
                                    + "' to be present in gRPC response");
                }
                assertJsonNodeContains(
                        actual.get(fieldName), expected.get(fieldName), path + "." + fieldName);
            }
            return;
        }
        if (expected.isArray()) {
            if (!actual.isArray()) {
                throw new AssertionError(
                        "Expected JSON array at '"
                                + path
                                + "' but actual was "
                                + actual.getNodeType());
            }
            if (actual.size() < expected.size()) {
                throw new AssertionError(
                        "Expected at least "
                                + expected.size()
                                + " entries at '"
                                + path
                                + "' but got "
                                + actual.size());
            }
            for (int i = 0; i < expected.size(); i++) {
                assertJsonNodeContains(actual.get(i), expected.get(i), path + "[" + i + "]");
            }
            return;
        }
        if (!actual.equals(expected)) {
            throw new AssertionError(
                    "Expected value at '" + path + "' to be " + expected + " but was " + actual);
        }
    }

    private JsonPath ensureGrpcJsonPath() {
        if (lastGrpcJsonPath == null) {
            throw new AssertionError("No gRPC response has been received");
        }
        return lastGrpcJsonPath;
    }

    private Metadata buildGrpcMetadata() {
        Metadata metadata = new Metadata();
        boolean hasEntries = false;
        if (currentUserId != null) {
            String token = keycloakClient.getAccessToken(currentUserId);
            metadata.put(AUTHORIZATION_METADATA, "Bearer " + token);
            hasEntries = true;
        }
        if (currentApiKey != null) {
            metadata.put(API_KEY_METADATA, currentApiKey);
            hasEntries = true;
        }
        return hasEntries ? metadata : null;
    }

    private Message callGrpcService(String serviceMethod, String body) throws Exception {
        String[] parts = serviceMethod.split("/");
        if (parts.length != 2) {
            throw new IllegalArgumentException(
                    "Expected gRPC target in the form Service/Method, got: " + serviceMethod);
        }
        String service = parts[0];
        String method = parts[1];

        Metadata metadata = buildGrpcMetadata();

        return switch (service) {
            case "SystemService" -> callSystemService(method, metadata, body);
            case "MessagesService" -> callMessagesService(method, metadata, body);
            case "SearchService" -> callSearchService(method, metadata, body);
            case "ConversationsService" -> callConversationsService(method, metadata, body);
            case "ConversationMembershipsService" ->
                    callConversationMembershipsService(method, metadata, body);
            case "ResponseResumerService" -> callResponseResumerService(method, metadata, body);
            default ->
                    throw new IllegalArgumentException(
                            "Unsupported gRPC service: " + service + " for method " + method);
        };
    }

    private Message callSystemService(String method, Metadata metadata, String body)
            throws Exception {
        if (!"GetHealth".equals(method)) {
            throw new IllegalArgumentException("Unsupported SystemService method: " + method);
        }
        var stub = SystemServiceGrpc.newBlockingStub(grpcChannel);
        if (metadata != null) {
            stub = stub.withInterceptors(MetadataUtils.newAttachHeadersInterceptor(metadata));
        }
        return stub.getHealth(Empty.newBuilder().build());
    }

    private Message callMessagesService(String method, Metadata metadata, String body)
            throws Exception {
        var stub = MessagesServiceGrpc.newBlockingStub(grpcChannel);
        if (metadata != null) {
            stub = stub.withInterceptors(MetadataUtils.newAttachHeadersInterceptor(metadata));
        }
        switch (method) {
            case "ListMessages":
                {
                    var requestBuilder = ListMessagesRequest.newBuilder();
                    if (body != null && !body.isBlank()) {
                        TextFormat.merge(body, requestBuilder);
                    }
                    return stub.listMessages(requestBuilder.build());
                }
            case "AppendMessage":
                {
                    var requestBuilder = AppendMessageRequest.newBuilder();
                    if (body != null && !body.isBlank()) {
                        TextFormat.merge(body, requestBuilder);
                    }
                    return stub.appendMessage(requestBuilder.build());
                }
            case "SyncMessages":
                {
                    var requestBuilder = SyncMessagesRequest.newBuilder();
                    if (body != null && !body.isBlank()) {
                        TextFormat.merge(body, requestBuilder);
                    }
                    return stub.syncMessages(requestBuilder.build());
                }
            default:
                throw new IllegalArgumentException("Unsupported MessagesService method: " + method);
        }
    }

    private Message callSearchService(String method, Metadata metadata, String body)
            throws Exception {
        var stub = SearchServiceGrpc.newBlockingStub(grpcChannel);
        if (metadata != null) {
            stub = stub.withInterceptors(MetadataUtils.newAttachHeadersInterceptor(metadata));
        }
        switch (method) {
            case "CreateSummary":
                {
                    var requestBuilder = CreateSummaryRequest.newBuilder();
                    if (body != null && !body.isBlank()) {
                        TextFormat.merge(body, requestBuilder);
                    }
                    return stub.createSummary(requestBuilder.build());
                }
            case "SearchMessages":
                {
                    var requestBuilder = SearchMessagesRequest.newBuilder();
                    if (body != null && !body.isBlank()) {
                        TextFormat.merge(body, requestBuilder);
                    }
                    return stub.searchMessages(requestBuilder.build());
                }
            default:
                throw new IllegalArgumentException("Unsupported SearchService method: " + method);
        }
    }

    private Message callConversationsService(String method, Metadata metadata, String body)
            throws Exception {
        var stub = ConversationsServiceGrpc.newBlockingStub(grpcChannel);
        if (metadata != null) {
            stub = stub.withInterceptors(MetadataUtils.newAttachHeadersInterceptor(metadata));
        }
        switch (method) {
            case "ListConversations":
                {
                    var requestBuilder = ListConversationsRequest.newBuilder();
                    if (body != null && !body.isBlank()) {
                        TextFormat.merge(body, requestBuilder);
                    }
                    return stub.listConversations(requestBuilder.build());
                }
            case "CreateConversation":
                {
                    var requestBuilder =
                            io.github.chirino.memory.grpc.v1.CreateConversationRequest.newBuilder();
                    if (body != null && !body.isBlank()) {
                        TextFormat.merge(body, requestBuilder);
                    }
                    Message response = stub.createConversation(requestBuilder.build());
                    if (response instanceof Conversation) {
                        Conversation conv = (Conversation) response;
                        if (conv.getId() != null && !conv.getId().isEmpty()) {
                            conversationId = conv.getId();
                            contextVariables.put("conversationId", conversationId);
                        }
                    }
                    return response;
                }
            case "GetConversation":
                {
                    var requestBuilder = GetConversationRequest.newBuilder();
                    if (body != null && !body.isBlank()) {
                        TextFormat.merge(body, requestBuilder);
                    }
                    return stub.getConversation(requestBuilder.build());
                }
            case "DeleteConversation":
                {
                    var requestBuilder = DeleteConversationRequest.newBuilder();
                    if (body != null && !body.isBlank()) {
                        TextFormat.merge(body, requestBuilder);
                    }
                    return stub.deleteConversation(requestBuilder.build());
                }
            case "ForkConversation":
                {
                    var requestBuilder = ForkConversationRequest.newBuilder();
                    if (body != null && !body.isBlank()) {
                        TextFormat.merge(body, requestBuilder);
                    }
                    Message response = stub.forkConversation(requestBuilder.build());
                    if (response instanceof Conversation) {
                        Conversation conv = (Conversation) response;
                        if (conv.getId() != null && !conv.getId().isEmpty()) {
                            contextVariables.put("forkedConversationId", conv.getId());
                        }
                    }
                    return response;
                }
            case "ListForks":
                {
                    var requestBuilder = ListForksRequest.newBuilder();
                    if (body != null && !body.isBlank()) {
                        TextFormat.merge(body, requestBuilder);
                    }
                    return stub.listForks(requestBuilder.build());
                }
            case "TransferOwnership":
                {
                    var requestBuilder = TransferOwnershipRequest.newBuilder();
                    if (body != null && !body.isBlank()) {
                        TextFormat.merge(body, requestBuilder);
                    }
                    return stub.transferOwnership(requestBuilder.build());
                }
            default:
                throw new IllegalArgumentException(
                        "Unsupported ConversationsService method: " + method);
        }
    }

    private Message callConversationMembershipsService(
            String method, Metadata metadata, String body) throws Exception {
        var stub = ConversationMembershipsServiceGrpc.newBlockingStub(grpcChannel);
        if (metadata != null) {
            stub = stub.withInterceptors(MetadataUtils.newAttachHeadersInterceptor(metadata));
        }
        switch (method) {
            case "ListMemberships":
                {
                    var requestBuilder = ListMembershipsRequest.newBuilder();
                    if (body != null && !body.isBlank()) {
                        TextFormat.merge(body, requestBuilder);
                    }
                    return stub.listMemberships(requestBuilder.build());
                }
            case "ShareConversation":
                {
                    var requestBuilder = ShareConversationRequest.newBuilder();
                    if (body != null && !body.isBlank()) {
                        TextFormat.merge(body, requestBuilder);
                    }
                    return stub.shareConversation(requestBuilder.build());
                }
            case "UpdateMembership":
                {
                    var requestBuilder = UpdateMembershipRequest.newBuilder();
                    if (body != null && !body.isBlank()) {
                        TextFormat.merge(body, requestBuilder);
                    }
                    return stub.updateMembership(requestBuilder.build());
                }
            case "DeleteMembership":
                {
                    var requestBuilder = DeleteMembershipRequest.newBuilder();
                    if (body != null && !body.isBlank()) {
                        TextFormat.merge(body, requestBuilder);
                    }
                    return stub.deleteMembership(requestBuilder.build());
                }
            default:
                throw new IllegalArgumentException(
                        "Unsupported ConversationMembershipsService method: " + method);
        }
    }

    private Message callResponseResumerService(String method, Metadata metadata, String body)
            throws Exception {
        var stub = ResponseResumerServiceGrpc.newBlockingStub(grpcChannel);
        if (metadata != null) {
            stub = stub.withInterceptors(MetadataUtils.newAttachHeadersInterceptor(metadata));
        }
        switch (method) {
            case "IsEnabled":
                {
                    return stub.isEnabled(Empty.newBuilder().build());
                }
            case "HasResponseInProgress":
                {
                    var requestBuilder = HasResponseInProgressRequest.newBuilder();
                    if (body != null && !body.isBlank()) {
                        TextFormat.merge(body, requestBuilder);
                    }
                    return stub.hasResponseInProgress(requestBuilder.build());
                }
            case "CheckConversations":
                {
                    var requestBuilder = CheckConversationsRequest.newBuilder();
                    if (body != null && !body.isBlank()) {
                        TextFormat.merge(body, requestBuilder);
                    }
                    return stub.checkConversations(requestBuilder.build());
                }
            case "StreamResponseTokens":
                {
                    // Note: This is a client streaming call, but we'll handle a single request
                    // for testing purposes. In production, this would be a stream of requests.
                    var requestBuilder = StreamResponseTokenRequest.newBuilder();
                    if (body != null && !body.isBlank()) {
                        TextFormat.merge(body, requestBuilder);
                    }
                    // For blocking stub, we can only send one request at a time
                    // The actual implementation expects a stream, but for testing we'll send one
                    var mutinyStub = MutinyResponseResumerServiceGrpc.newMutinyStub(grpcChannel);
                    if (metadata != null) {
                        mutinyStub =
                                mutinyStub.withInterceptors(
                                        MetadataUtils.newAttachHeadersInterceptor(metadata));
                    }
                    var request = requestBuilder.build();
                    var requestStream = io.smallrye.mutiny.Multi.createFrom().item(request);
                    return mutinyStub.streamResponseTokens(requestStream).await().indefinitely();
                }
            case "ReplayResponseTokens":
                {
                    // Note: This is a server streaming call. For testing, we'll get the first
                    // response.
                    var requestBuilder = ReplayResponseTokensRequest.newBuilder();
                    if (body != null && !body.isBlank()) {
                        TextFormat.merge(body, requestBuilder);
                    }
                    // For blocking stub, we can iterate through the stream
                    var request = requestBuilder.build();
                    Iterator<ReplayResponseTokensResponse> responses =
                            stub.replayResponseTokens(request);
                    if (responses.hasNext()) {
                        return responses.next();
                    }
                    // Return an empty response if no tokens
                    return ReplayResponseTokensResponse.newBuilder().build();
                }
            default:
                throw new IllegalArgumentException(
                        "Unsupported ResponseResumerService method: " + method);
        }
    }

    private GrpcEndpoint resolveGrpcEndpoint() {
        Config config = ConfigProvider.getConfig();
        if (config.getOptionalValue("quarkus.grpc.server.enabled", Boolean.class).orElse(false)) {
            String host =
                    config.getOptionalValue("quarkus.grpc.server.host", String.class)
                            .orElse("localhost");
            int port =
                    config.getOptionalValue("quarkus.grpc.server.test-port", Integer.class)
                            .orElse(
                                    config.getOptionalValue(
                                                    "quarkus.grpc.server.port", Integer.class)
                                            .orElse(9000));
            return new GrpcEndpoint(host, port);
        }
        String target =
                config.getOptionalValue("test.url", String.class).orElse("http://localhost:8081");
        URI uri;
        try {
            uri = new URI(target);
        } catch (URISyntaxException e) {
            throw new IllegalStateException("Invalid test.url configuration: " + target, e);
        }
        String host = uri.getHost() != null ? uri.getHost() : "localhost";
        int port =
                uri.getPort() != -1
                        ? uri.getPort()
                        : ("https".equalsIgnoreCase(uri.getScheme()) ? 443 : 80);
        return new GrpcEndpoint(host, port);
    }

    private record GrpcEndpoint(String host, int port) {}

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
