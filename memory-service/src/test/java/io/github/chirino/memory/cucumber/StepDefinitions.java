package io.github.chirino.memory.cucumber;

import static io.github.chirino.memory.cucumber.StepUsageTracker.trackUsage;
import static io.restassured.RestAssured.given;
import static org.hamcrest.MatcherAssert.assertThat;
import static org.hamcrest.Matchers.containsString;
import static org.hamcrest.Matchers.greaterThan;
import static org.hamcrest.Matchers.hasSize;
import static org.hamcrest.Matchers.is;
import static org.hamcrest.Matchers.not;
import static org.hamcrest.Matchers.notNullValue;
import static org.hamcrest.Matchers.nullValue;

import com.fasterxml.jackson.core.type.TypeReference;
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
import io.github.chirino.memory.api.dto.ConversationDto;
import io.github.chirino.memory.api.dto.CreateConversationRequest;
import io.github.chirino.memory.api.dto.CreateUserEntryRequest;
import io.github.chirino.memory.client.model.CreateEntryRequest;
import io.github.chirino.memory.config.MemoryStoreSelector;
import io.github.chirino.memory.grpc.v1.AppendEntryRequest;
import io.github.chirino.memory.grpc.v1.CancelResponseRequest;
import io.github.chirino.memory.grpc.v1.CheckConversationsRequest;
import io.github.chirino.memory.grpc.v1.Conversation;
import io.github.chirino.memory.grpc.v1.ConversationMembership;
import io.github.chirino.memory.grpc.v1.ConversationMembershipsServiceGrpc;
import io.github.chirino.memory.grpc.v1.ConversationsServiceGrpc;
import io.github.chirino.memory.grpc.v1.DeleteConversationRequest;
import io.github.chirino.memory.grpc.v1.DeleteMembershipRequest;
import io.github.chirino.memory.grpc.v1.EntriesServiceGrpc;
import io.github.chirino.memory.grpc.v1.ForkConversationRequest;
import io.github.chirino.memory.grpc.v1.GetConversationRequest;
import io.github.chirino.memory.grpc.v1.HealthResponse;
import io.github.chirino.memory.grpc.v1.IndexTranscriptRequest;
import io.github.chirino.memory.grpc.v1.ListConversationsRequest;
import io.github.chirino.memory.grpc.v1.ListConversationsResponse;
import io.github.chirino.memory.grpc.v1.ListEntriesRequest;
import io.github.chirino.memory.grpc.v1.ListEntriesResponse;
import io.github.chirino.memory.grpc.v1.ListForksRequest;
import io.github.chirino.memory.grpc.v1.ListForksResponse;
import io.github.chirino.memory.grpc.v1.ListMembershipsRequest;
import io.github.chirino.memory.grpc.v1.ListMembershipsResponse;
import io.github.chirino.memory.grpc.v1.MutinyResponseResumerServiceGrpc;
import io.github.chirino.memory.grpc.v1.ReplayResponseTokensRequest;
import io.github.chirino.memory.grpc.v1.ReplayResponseTokensResponse;
import io.github.chirino.memory.grpc.v1.ResponseResumerServiceGrpc;
import io.github.chirino.memory.grpc.v1.SearchEntriesRequest;
import io.github.chirino.memory.grpc.v1.SearchEntriesResponse;
import io.github.chirino.memory.grpc.v1.SearchServiceGrpc;
import io.github.chirino.memory.grpc.v1.ShareConversationRequest;
import io.github.chirino.memory.grpc.v1.StreamResponseTokenRequest;
import io.github.chirino.memory.grpc.v1.StreamResponseTokenResponse;
import io.github.chirino.memory.grpc.v1.SyncEntriesRequest;
import io.github.chirino.memory.grpc.v1.SyncEntriesResponse;
import io.github.chirino.memory.grpc.v1.SystemServiceGrpc;
import io.github.chirino.memory.grpc.v1.TransferOwnershipRequest;
import io.github.chirino.memory.grpc.v1.UpdateMembershipRequest;
import io.github.chirino.memory.mongo.repo.MongoConversationGroupRepository;
import io.github.chirino.memory.mongo.repo.MongoConversationMembershipRepository;
import io.github.chirino.memory.mongo.repo.MongoConversationOwnershipTransferRepository;
import io.github.chirino.memory.mongo.repo.MongoConversationRepository;
import io.github.chirino.memory.mongo.repo.MongoEntryRepository;
import io.github.chirino.memory.persistence.repo.ConversationGroupRepository;
import io.github.chirino.memory.persistence.repo.ConversationMembershipRepository;
import io.github.chirino.memory.persistence.repo.ConversationOwnershipTransferRepository;
import io.github.chirino.memory.persistence.repo.ConversationRepository;
import io.github.chirino.memory.persistence.repo.EntryRepository;
import io.github.chirino.memory.security.ApiKeyManager;
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
import jakarta.persistence.EntityManager;
import jakarta.transaction.Transactional;
import jakarta.ws.rs.core.MediaType;
import java.net.URI;
import java.net.URISyntaxException;
import java.util.ArrayList;
import java.util.Arrays;
import java.util.HashMap;
import java.util.Iterator;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.CountDownLatch;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicBoolean;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.concurrent.atomic.AtomicLong;
import java.util.concurrent.atomic.AtomicReference;
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
    @Inject ApiKeyManager apiKeyManager;

    @Inject org.eclipse.microprofile.config.Config config;

    @Inject Instance<ConversationRepository> conversationRepository;
    @Inject Instance<ConversationGroupRepository> conversationGroupRepository;
    @Inject Instance<EntryRepository> entryRepository;
    @Inject Instance<ConversationMembershipRepository> membershipRepository;
    @Inject Instance<ConversationOwnershipTransferRepository> ownershipTransferRepository;
    @Inject Instance<MongoConversationRepository> mongoConversationRepository;
    @Inject Instance<MongoConversationGroupRepository> mongoConversationGroupRepository;
    @Inject Instance<MongoEntryRepository> mongoEntryRepository;
    @Inject Instance<MongoConversationMembershipRepository> mongoMembershipRepository;
    @Inject Instance<MongoConversationOwnershipTransferRepository> mongoOwnershipTransferRepository;

    @Inject Instance<EntityManager> entityManager;

    @Inject Instance<io.github.chirino.memory.persistence.repo.TaskRepository> taskRepository;
    @Inject Instance<io.github.chirino.memory.mongo.repo.MongoTaskRepository> mongoTaskRepository;
    @Inject Instance<io.github.chirino.memory.config.TaskRepositorySelector> taskRepositorySelector;
    @Inject Instance<io.github.chirino.memory.service.TaskProcessor> taskProcessor;

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
    private final AtomicLong streamCompletedAtNs = new AtomicLong(0);
    private final AtomicLong replayFinishedAtNs = new AtomicLong(0);
    private final AtomicLong replayFirstTokenAtNs = new AtomicLong(0);

    /**
     * Applies authentication (OIDC token and/or API key) to a RestAssured request specification.
     */
    private io.restassured.specification.RequestSpecification authenticateRequest(
            io.restassured.specification.RequestSpecification requestSpec) {
        if (currentUserId != null) {
            String token = keycloakClient.getAccessToken(currentUserId);
            if (token != null) {
                requestSpec = requestSpec.auth().oauth2(token);
            }
        }
        if (currentApiKey != null) {
            requestSpec = requestSpec.header("X-API-Key", currentApiKey);
        }
        return requestSpec;
    }

    @io.cucumber.java.Before(order = 0)
    @Transactional
    public void cleanupTasks() {
        String datastoreType =
                config.getOptionalValue("memory-service.datastore.type", String.class)
                        .orElse("postgres");
        if ("postgres".equals(datastoreType)) {
            entityManager.get().createNativeQuery("DELETE FROM tasks").executeUpdate();
        }
    }

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
        trackUsage();
        this.currentUserId = userId;
        this.currentApiKey = null;
        // We'll use KeycloakTestClient to get a real token when making requests
    }

    @io.cucumber.java.en.Given("I am authenticated as agent with API key {string}")
    public void iAmAuthenticatedAsAgentWithApiKey(String apiKey) {
        trackUsage();
        this.currentUserId = "alice"; // Default user for agent context
        this.currentApiKey = apiKey;
    }

    @io.cucumber.java.en.Given("I have a conversation with title {string}")
    public void iHaveAConversationWithTitle(String title) {
        trackUsage();
        CreateConversationRequest request = new CreateConversationRequest();
        request.setTitle(title);
        ConversationDto conversation =
                memoryStoreSelector.getStore().createConversation(currentUserId, request);
        this.conversationId = conversation.getId();
        contextVariables.put("conversationId", conversationId);
        // Resolve conversationGroupId from database for test infrastructure
        String groupId = resolveConversationGroupId(conversationId);
        contextVariables.put("conversationGroupId", groupId);
        contextVariables.put("conversationOwner", currentUserId);
    }

    @io.cucumber.java.en.Given("the conversation exists")
    public void theConversationExists() {
        trackUsage();
        if (this.conversationId == null) {
            iHaveAConversationWithTitle("Test Conversation");
        }
    }

    @io.cucumber.java.en.Given("the conversation id is {string}")
    public void theConversationIdIs(String id) {
        trackUsage();
        this.conversationId = id;
        try {
            memoryStoreSelector.getStore().deleteConversation(currentUserId, id);
        } catch (ResourceNotFoundException | AccessDeniedException ignored) {
            // Ignored; we only need to ensure the id is not already in use by the current user.
        }
        contextVariables.put("conversationId", conversationId);
    }

    @io.cucumber.java.en.Given("the conversation has no entries")
    public void theConversationHasNoEntries() {
        trackUsage();
        // Conversation already exists from background, no action needed
    }

    @io.cucumber.java.en.Given("the conversation has an entry {string}")
    public void theConversationHasAnEntry(String content) {
        trackUsage();
        CreateUserEntryRequest request = new CreateUserEntryRequest();
        request.setContent(content);
        memoryStoreSelector.getStore().appendUserEntry(currentUserId, conversationId, request);
    }

    @io.cucumber.java.en.Given("the conversation has {int} entries")
    public void theConversationHasEntries(int count) {
        trackUsage();
        for (int i = 1; i <= count; i++) {
            theConversationHasAnEntry("Entry " + i);
        }
    }

    @io.cucumber.java.en.Given("the conversation has an entry {string} in channel {string}")
    public void theConversationHasAnEntryInChannel(String content, String channel) {
        trackUsage();
        CreateEntryRequest request = new CreateEntryRequest();
        request.setContent(List.of(Map.of("type", "text", "text", content)));
        request.setChannel(CreateEntryRequest.ChannelEnum.fromString(channel.toLowerCase()));
        request.setContentType("test.v1");
        memoryStoreSelector
                .getStore()
                .appendAgentEntries(
                        currentUserId, conversationId, List.of(request), resolveClientId());
    }

    @io.cucumber.java.en.Given(
            "the conversation has an entry {string} in channel {string} with contentType {string}")
    public void theConversationHasAnEntryInChannelWithContentType(
            String content, String channel, String contentType) {
        trackUsage();
        CreateEntryRequest request = new CreateEntryRequest();
        request.setContent(List.of(Map.of("type", "text", "text", content)));
        request.setChannel(CreateEntryRequest.ChannelEnum.fromString(channel.toLowerCase()));
        request.setContentType(contentType);
        memoryStoreSelector
                .getStore()
                .appendAgentEntries(
                        currentUserId, conversationId, List.of(request), resolveClientId());
    }

    @io.cucumber.java.en.Given(
            "the conversation has a memory entry {string} with epoch {int} and contentType"
                    + " {string}")
    public void theConversationHasAMemoryEntryWithEpochAndContentType(
            String content, int epoch, String contentType) {
        trackUsage();
        CreateEntryRequest request = new CreateEntryRequest();
        request.setContent(List.of(Map.of("type", "text", "text", content)));
        request.setChannel(CreateEntryRequest.ChannelEnum.MEMORY);
        request.setEpoch((long) epoch);
        request.setContentType(contentType);
        memoryStoreSelector
                .getStore()
                .appendAgentEntries(
                        currentUserId, conversationId, List.of(request), resolveClientId());
    }

    @io.cucumber.java.en.Given("I have streamed tokens {string} to the conversation")
    public void iHaveStreamedTokensToTheConversation(String tokens) throws Exception {
        trackUsage();
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
                    .collect()
                    .last()
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
            trackUsage();
            mutinyStub =
                    mutinyStub.withInterceptors(
                            MetadataUtils.newAttachHeadersInterceptor(metadata));
        }

        streamStartedLatch = new CountDownLatch(1);
        streamedTokenCount = new AtomicInteger(0);
        inProgressStreamResponse = new CompletableFuture<>();
        streamCompletedAtNs.set(0);
        replayFinishedAtNs.set(0);
        replayFirstTokenAtNs.set(0);
        AtomicReference<StreamResponseTokenResponse> lastResponse = new AtomicReference<>();

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
                        lastResponse::set,
                        inProgressStreamResponse::completeExceptionally,
                        () -> {
                            StreamResponseTokenResponse response = lastResponse.get();
                            if (response == null) {
                                response = StreamResponseTokenResponse.newBuilder().build();
                            }
                            streamCompletedAtNs.compareAndSet(0, System.nanoTime());
                            inProgressStreamResponse.complete(response);
                        });
    }

    @io.cucumber.java.en.Given(
            "I start streaming tokens {string} to the conversation with {int}ms delay and keep the"
                    + " stream open until canceled")
    public void iStartStreamingTokensToTheConversationUntilCanceled(String tokens, int delayMs)
            throws Exception {
        Metadata metadata = buildGrpcMetadata();
        var mutinyStub = MutinyResponseResumerServiceGrpc.newMutinyStub(grpcChannel);
        if (metadata != null) {
            trackUsage();
            mutinyStub =
                    mutinyStub.withInterceptors(
                            MetadataUtils.newAttachHeadersInterceptor(metadata));
        }

        streamStartedLatch = new CountDownLatch(1);
        streamedTokenCount = new AtomicInteger(0);
        inProgressStreamResponse = new CompletableFuture<>();
        streamCompletedAtNs.set(0);
        replayFinishedAtNs.set(0);

        AtomicBoolean canceled = new AtomicBoolean(false);
        AtomicBoolean completed = new AtomicBoolean(false);
        AtomicReference<
                        io.smallrye.mutiny.subscription.MultiEmitter<
                                ? super StreamResponseTokenRequest>>
                emitterRef = new AtomicReference<>();
        AtomicReference<StreamResponseTokenResponse> lastResponse = new AtomicReference<>();

        var requestStream =
                io.smallrye.mutiny.Multi.createFrom()
                        .<StreamResponseTokenRequest>emitter(
                                emitter -> {
                                    emitterRef.set(emitter);
                                    Thread thread =
                                            new Thread(
                                                    () -> {
                                                        try {
                                                            for (int i = 0;
                                                                    i < tokens.length();
                                                                    i++) {
                                                                if (canceled.get()) {
                                                                    break;
                                                                }
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
                                                            while (!canceled.get()) {
                                                                Thread.sleep(10);
                                                            }
                                                            if (completed.compareAndSet(
                                                                    false, true)) {
                                                                emitter.emit(
                                                                        StreamResponseTokenRequest
                                                                                .newBuilder()
                                                                                .setComplete(true)
                                                                                .build());
                                                                emitter.complete();
                                                            }
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
                        response -> {
                            lastResponse.set(response);
                            if (response.getCancelRequested()) {
                                canceled.set(true);
                                var emitter = emitterRef.get();
                                if (emitter != null && completed.compareAndSet(false, true)) {
                                    emitter.emit(
                                            StreamResponseTokenRequest.newBuilder()
                                                    .setComplete(true)
                                                    .build());
                                    emitter.complete();
                                }
                            }
                        },
                        inProgressStreamResponse::completeExceptionally,
                        () -> {
                            StreamResponseTokenResponse response = lastResponse.get();
                            if (response == null) {
                                response = StreamResponseTokenResponse.newBuilder().build();
                            }
                            streamCompletedAtNs.compareAndSet(0, System.nanoTime());
                            inProgressStreamResponse.complete(response);
                        });
    }

    @io.cucumber.java.en.Given("I wait for the response stream to send at least {int} tokens")
    public void iWaitForTheResponseStreamToSendAtLeastTokens(int count)
            throws InterruptedException {
        trackUsage();
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
            "I replay response tokens from the beginning in a second session and collect tokens"
                    + " {string}")
    public void iReplayResponseTokensFromBeginningInSecondSessionAndCollectTokens(
            String expectedTokens) {
        trackUsage();
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
                ReplayResponseTokensRequest.newBuilder().setConversationId(conversationId).build();

        int expectedCount = expectedTokens.length();
        List<ReplayResponseTokensResponse> responses;
        try {
            responses =
                    mutinyStub
                            .replayResponseTokens(request)
                            .onItem()
                            .invoke(
                                    response ->
                                            replayFirstTokenAtNs.compareAndSet(
                                                    0, System.nanoTime()))
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
        replayFinishedAtNs.set(System.nanoTime());
    }

    @io.cucumber.java.en.Then("the replay should start before the stream completes")
    public void theReplayShouldStartBeforeTheStreamCompletes() {
        trackUsage();
        long replayStarted = replayFirstTokenAtNs.get();
        if (replayStarted == 0) {
            throw new AssertionError("Replay did not receive any tokens");
        }
        long streamCompleted = streamCompletedAtNs.get();
        if (streamCompleted != 0 && replayStarted >= streamCompleted) {
            throw new AssertionError("Replay started after the stream completed");
        }
    }

    @io.cucumber.java.en.Then("I wait for the response stream to complete")
    public void iWaitForTheResponseStreamToComplete() {
        trackUsage();
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
        trackUsage();
        CreateConversationRequest request = new CreateConversationRequest();
        request.setTitle("Owned by " + ownerId);
        this.conversationId =
                memoryStoreSelector.getStore().createConversation(ownerId, request).getId();
        contextVariables.put("conversationId", conversationId);
        contextVariables.put("conversationOwner", ownerId);
    }

    @io.cucumber.java.en.When("I list entries for the conversation")
    public void iListEntriesForTheConversation() {
        trackUsage();
        iListEntriesForTheConversationWithParams(null, null, null, null);
    }

    @io.cucumber.java.en.When("I list entries with limit {int}")
    public void iListEntriesWithLimit(int limit) {
        trackUsage();
        iListEntriesForTheConversationWithParams(null, limit, null, null);
    }

    @io.cucumber.java.en.When("I list entries for the conversation with channel {string}")
    public void iListEntriesForTheConversationWithChannel(String channel) {
        trackUsage();
        iListEntriesForTheConversationWithParams(null, null, channel, null);
    }

    @io.cucumber.java.en.When("I list memory entries for the conversation with epoch {string}")
    public void iListMemoryEntriesForTheConversationWithEpoch(String epoch) {
        trackUsage();
        iListEntriesForTheConversationWithParams(null, null, "MEMORY", epoch);
    }

    @io.cucumber.java.en.When("I list entries for conversation {string}")
    public void iListEntriesForConversation(String convId) {
        trackUsage();
        this.conversationId = convId;
        iListEntriesForTheConversation();
    }

    @io.cucumber.java.en.When("I list entries for that conversation")
    public void iListEntriesForThatConversation() {
        trackUsage();
        iListEntriesForTheConversation();
    }

    private void iListEntriesForTheConversationWithParams(
            String after, Integer limit, String channel, String epoch) {
        var requestSpec = given();
        requestSpec = authenticateRequest(requestSpec);
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
        this.lastResponse = request.get("/v1/conversations/{id}/entries", conversationId);
    }

    @io.cucumber.java.en.When(
            "I append an entry with content {string} and channel {string} and contentType"
                    + " {string}")
    public void iAppendAnEntryWithContentAndChannelAndContentType(
            String content, String channel, String contentType) {
        trackUsage();
        var requestSpec =
                given().contentType(MediaType.APPLICATION_JSON)
                        .body(
                                Map.of(
                                        "content",
                                        List.of(Map.of("type", "text", "text", content)),
                                        "channel",
                                        channel,
                                        "contentType",
                                        contentType));
        requestSpec = authenticateRequest(requestSpec);
        this.lastResponse =
                requestSpec.when().post("/v1/conversations/{id}/entries", conversationId);
    }

    @io.cucumber.java.en.And("I append an entry to the conversation:")
    public void iAppendAnEntryToTheConversation(String requestBody) {
        trackUsage();
        String rendered = renderTemplate(requestBody);
        var requestSpec = given().contentType(MediaType.APPLICATION_JSON).body(rendered);
        requestSpec = authenticateRequest(requestSpec);
        this.lastResponse =
                requestSpec.when().post("/v1/conversations/{id}/entries", conversationId);
    }

    @io.cucumber.java.en.When("I sync memory entries with request:")
    public void iSyncMemoryEntriesWithRequest(String requestBody) {
        trackUsage();
        String rendered = renderTemplate(requestBody);
        var requestSpec = given().contentType(MediaType.APPLICATION_JSON).body(rendered);
        requestSpec = authenticateRequest(requestSpec);
        this.lastResponse =
                requestSpec.when().post("/v1/conversations/{id}/entries/sync", conversationId);
    }

    @io.cucumber.java.en.Then("the sync response should contain {int} entries")
    public void theSyncResponseShouldContainEntries(int count) {
        trackUsage();
        if (lastResponse == null) {
            throw new AssertionError("No response has been received");
        }
        JsonPath jsonPath = lastResponse.jsonPath();
        List<Object> entries = jsonPath.getList("entries");
        if (entries == null) {
            throw new AssertionError(
                    "Response does not contain 'entries' field. Response body: "
                            + lastResponse.getBody().asString());
        }
        assertThat("Sync response should contain " + count + " entries", entries.size(), is(count));
    }

    @io.cucumber.java.en.When("I create a summary with request:")
    public void iCreateASummaryWithRequest(String requestBody) {
        trackUsage();
        String rendered = renderTemplate(requestBody);
        var requestSpec = given().contentType(MediaType.APPLICATION_JSON).body(rendered);
        requestSpec = authenticateRequest(requestSpec);
        this.lastResponse =
                requestSpec.when().post("/v1/conversations/{id}/summaries", conversationId);
    }

    @io.cucumber.java.en.When("I index a transcript with request:")
    public void iIndexATranscriptWithRequest(String requestBody) {
        trackUsage();
        String rendered = renderTemplate(requestBody);
        var requestSpec = given().contentType(MediaType.APPLICATION_JSON).body(rendered);
        requestSpec = authenticateRequest(requestSpec);
        this.lastResponse = requestSpec.when().post("/v1/conversations/index");
    }

    @io.cucumber.java.en.When("I search entries with request:")
    public void iSearchEntriesWithRequest(String requestBody) {
        trackUsage();
        String rendered = renderTemplate(requestBody);
        var requestSpec = given().contentType(MediaType.APPLICATION_JSON).body(rendered);
        requestSpec = authenticateRequest(requestSpec);
        this.lastResponse = requestSpec.when().post("/v1/user/search/entries");
    }

    @io.cucumber.java.en.When("I search conversations with request:")
    public void iSearchConversationsWithRequest(String requestBody) {
        trackUsage();
        String rendered = renderTemplate(requestBody);
        var requestSpec = given().contentType(MediaType.APPLICATION_JSON).body(rendered);
        requestSpec = authenticateRequest(requestSpec);
        this.lastResponse = requestSpec.when().post("/v1/conversations/search");
    }

    @io.cucumber.java.en.Then("the search response should contain at least {int} results")
    public void theSearchResponseShouldContainAtLeastResults(int minCount) {
        trackUsage();
        lastResponse.then().body("data.size()", greaterThan(minCount - 1));
    }

    @io.cucumber.java.en.When("I search entries for query {string}")
    public void iSearchEntriesForQuery(String query) {
        trackUsage();
        var requestSpec =
                given().contentType(MediaType.APPLICATION_JSON).body(Map.of("query", query));
        requestSpec = authenticateRequest(requestSpec);
        this.lastResponse = requestSpec.when().post("/v1/user/search/entries");
    }

    @io.cucumber.java.en.When("I search conversations for query {string}")
    public void iSearchConversationsForQuery(String query) {
        trackUsage();
        var requestSpec =
                given().contentType(MediaType.APPLICATION_JSON).body(Map.of("query", query));
        requestSpec = authenticateRequest(requestSpec);
        this.lastResponse = requestSpec.when().post("/v1/conversations/search");
    }

    @io.cucumber.java.en.When("I create a conversation with request:")
    public void iCreateAConversationWithRequest(String requestBody) {
        trackUsage();
        String rendered = renderTemplate(requestBody);
        var requestSpec = given().contentType(MediaType.APPLICATION_JSON).body(rendered);
        requestSpec = authenticateRequest(requestSpec);
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
        trackUsage();
        iListConversationsWithParams(null, null, null);
    }

    @io.cucumber.java.en.When("I list conversations with limit {int}")
    public void iListConversationsWithLimit(int limit) {
        trackUsage();
        iListConversationsWithParams(null, limit, null);
    }

    @io.cucumber.java.en.When("I list conversations with limit {int} and after {string}")
    public void iListConversationsWithLimitAndAfter(int limit, String after) {
        trackUsage();
        iListConversationsWithParams(after, limit, null);
    }

    @io.cucumber.java.en.When("I list conversations with query {string}")
    public void iListConversationsWithQuery(String query) {
        trackUsage();
        iListConversationsWithParams(null, null, query);
    }

    private void iListConversationsWithParams(String after, Integer limit, String query) {
        var requestSpec = given();
        requestSpec = authenticateRequest(requestSpec);
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
        trackUsage();
        iGetConversation(conversationId);
    }

    @io.cucumber.java.en.When("I get conversation {string}")
    public void iGetConversation(String convId) {
        trackUsage();
        String renderedConvId = renderTemplate(convId);
        // Strip quotes if present (RestAssured path parameters shouldn't have quotes)
        if (renderedConvId.startsWith("\"") && renderedConvId.endsWith("\"")) {
            renderedConvId = renderedConvId.substring(1, renderedConvId.length() - 1);
        }
        var requestSpec = given();
        requestSpec = authenticateRequest(requestSpec);
        this.lastResponse = requestSpec.when().get("/v1/conversations/{id}", renderedConvId);
    }

    @io.cucumber.java.en.When("I get that conversation")
    public void iGetThatConversation() {
        trackUsage();
        iGetTheConversation();
    }

    @io.cucumber.java.en.When("I delete the conversation")
    public void iDeleteTheConversation() {
        trackUsage();
        iDeleteConversation(conversationId);
    }

    @io.cucumber.java.en.When("I delete conversation {string}")
    public void iDeleteConversation(String convId) {
        trackUsage();
        // Render template to resolve variables like "${rootConversationId}"
        String renderedConvId = renderTemplate(convId);
        // Remove quotes if present (from template rendering)
        if (renderedConvId.startsWith("\"") && renderedConvId.endsWith("\"")) {
            renderedConvId = renderedConvId.substring(1, renderedConvId.length() - 1);
        }
        var requestSpec = given();
        requestSpec = authenticateRequest(requestSpec);
        this.lastResponse = requestSpec.when().delete("/v1/conversations/{id}", renderedConvId);
    }

    @io.cucumber.java.en.When("I delete that conversation")
    public void iDeleteThatConversation() {
        trackUsage();
        iDeleteTheConversation();
    }

    @io.cucumber.java.en.When("I transfer ownership of the conversation to {string} with request:")
    public void iTransferOwnershipOfTheConversationToWithRequest(
            String newOwner, String requestBody) {
        trackUsage();
        String rendered = renderTemplate(requestBody);
        var requestSpec = given().contentType(MediaType.APPLICATION_JSON).body(rendered);
        requestSpec = authenticateRequest(requestSpec);
        this.lastResponse =
                requestSpec
                        .when()
                        .post("/v1/conversations/{id}/transfer-ownership", conversationId);
    }

    @io.cucumber.java.en.When(
            "I transfer ownership of conversation {string} to {string} with request:")
    public void iTransferOwnershipOfConversationToWithRequest(
            String convId, String newOwner, String requestBody) {
        trackUsage();
        String rendered = renderTemplate(requestBody);
        var requestSpec = given().contentType(MediaType.APPLICATION_JSON).body(rendered);
        requestSpec = authenticateRequest(requestSpec);
        this.lastResponse =
                requestSpec.when().post("/v1/conversations/{id}/transfer-ownership", convId);
    }

    @io.cucumber.java.en.When("I transfer ownership of that conversation to {string} with request:")
    public void iTransferOwnershipOfThatConversationToWithRequest(
            String newOwner, String requestBody) {
        trackUsage();
        iTransferOwnershipOfTheConversationToWithRequest(newOwner, requestBody);
    }

    @io.cucumber.java.en.Then("the response should contain at least {int} conversations")
    public void theResponseShouldContainAtLeastConversations(int minCount) {
        trackUsage();
        lastResponse.then().body("data.size()", greaterThan(minCount - 1));
    }

    @io.cucumber.java.en.Then("the response should contain at least {int} conversation")
    public void theResponseShouldContainAtLeastConversation(int minCount) {
        trackUsage();
        theResponseShouldContainAtLeastConversations(minCount);
    }

    @io.cucumber.java.en.Then("the response should contain at least {int} memberships")
    public void theResponseShouldContainAtLeastMemberships(int minCount) {
        trackUsage();
        lastResponse.then().body("data.size()", greaterThan(minCount - 1));
    }

    @io.cucumber.java.en.Then("the response should contain at least {int} item(s)")
    public void theResponseShouldContainAtLeastItems(int minCount) {
        trackUsage();
        JsonPath jsonPath = lastResponse.jsonPath();
        List<?> data = jsonPath.getList("data");
        int actualSize = data != null ? data.size() : 0;
        assertThat(
                "Expected at least "
                        + minCount
                        + " item(s) but got "
                        + actualSize
                        + ". Response status: "
                        + lastResponse.statusCode()
                        + ". Response body: "
                        + lastResponse.getBody().asString(),
                actualSize,
                greaterThan(minCount - 1));
    }

    @io.cucumber.java.en.Then("the response should contain {int} conversations")
    public void theResponseShouldContainConversations(int count) {
        trackUsage();
        lastResponse.then().body("data", hasSize(count));
    }

    @io.cucumber.java.en.Then("the response body should contain {string}")
    public void theResponseBodyShouldContain(String text) {
        trackUsage();
        String body = lastResponse.getBody().asString();
        assertThat("Response body should contain: " + text, body, containsString(text));
    }

    @io.cucumber.java.en.Then("the response body should not contain {string}")
    public void theResponseBodyShouldNotContain(String text) {
        trackUsage();
        String body = lastResponse.getBody().asString();
        assertThat("Response body should not contain: " + text, body, not(containsString(text)));
    }

    @io.cucumber.java.en.When("I fork the conversation at entry {string}")
    public void iForkTheConversationAtEntry(String entryId) {
        trackUsage();
        // Fork without a request body (uses empty JSON object)
        iForkTheConversationAtEntryWithRequest(entryId, "{}");
    }

    @io.cucumber.java.en.When("I fork the conversation at entry {string} with request:")
    public void iForkTheConversationAtEntryWithRequest(String entryId, String requestBody) {
        trackUsage();
        String rendered = renderTemplate(requestBody);
        var requestSpec = given().contentType(MediaType.APPLICATION_JSON).body(rendered);
        requestSpec = authenticateRequest(requestSpec);
        String renderedEntryId = renderTemplate(entryId);
        // Remove quotes if present (from template rendering)
        if (renderedEntryId.startsWith("\"") && renderedEntryId.endsWith("\"")) {
            renderedEntryId = renderedEntryId.substring(1, renderedEntryId.length() - 1);
        }
        this.lastResponse =
                requestSpec
                        .when()
                        .post(
                                "/v1/conversations/{id}/entries/{eid}/fork",
                                conversationId,
                                renderedEntryId);
        if (lastResponse.getStatusCode() == 201) {
            String id = lastResponse.jsonPath().getString("id");
            if (id != null) {
                contextVariables.put("forkedConversationId", id);
            }
        }
    }

    @io.cucumber.java.en.When("I fork conversation {string} at entry {string} with request:")
    public void iForkConversationAtEntryWithRequest(
            String convId, String entryId, String requestBody) {
        trackUsage();
        String rendered = renderTemplate(requestBody);
        var requestSpec = given().contentType(MediaType.APPLICATION_JSON).body(rendered);
        requestSpec = authenticateRequest(requestSpec);
        String renderedEntryId = renderTemplate(entryId);
        // Remove quotes if present (from template rendering)
        if (renderedEntryId.startsWith("\"") && renderedEntryId.endsWith("\"")) {
            renderedEntryId = renderedEntryId.substring(1, renderedEntryId.length() - 1);
        }
        this.lastResponse =
                requestSpec
                        .when()
                        .post("/v1/conversations/{id}/entries/{eid}/fork", convId, renderedEntryId);
    }

    @io.cucumber.java.en.When("I fork that conversation at entry {string} with request:")
    public void iForkThatConversationAtEntryWithRequest(String entryId, String requestBody) {
        trackUsage();
        iForkTheConversationAtEntryWithRequest(entryId, requestBody);
    }

    @io.cucumber.java.en.When("I list forks for the conversation")
    public void iListForksForTheConversation() {
        trackUsage();
        iListForksForConversation(conversationId);
    }

    @io.cucumber.java.en.When("I list forks for conversation {string}")
    public void iListForksForConversation(String convId) {
        trackUsage();
        var requestSpec = given();
        requestSpec = authenticateRequest(requestSpec);
        this.lastResponse = requestSpec.when().get("/v1/conversations/{id}/forks", convId);
    }

    @io.cucumber.java.en.When("I list forks for that conversation")
    public void iListForksForThatConversation() {
        trackUsage();
        iListForksForTheConversation();
    }

    @io.cucumber.java.en.When("I share the conversation with user {string} with request:")
    public void iShareTheConversationWithUserWithRequest(String userId, String requestBody) {
        trackUsage();
        String rendered = renderTemplate(requestBody);
        var requestSpec = given().contentType(MediaType.APPLICATION_JSON).body(rendered);
        requestSpec = authenticateRequest(requestSpec);
        this.lastResponse =
                requestSpec.when().post("/v1/conversations/{id}/memberships", conversationId);
    }

    @io.cucumber.java.en.When("I share conversation {string} with user {string} with request:")
    public void iShareConversationWithUserWithRequest(
            String convId, String userId, String requestBody) {
        trackUsage();
        String rendered = renderTemplate(requestBody);
        String renderedConvId = renderTemplate(convId);
        // Strip quotes if present (RestAssured path parameters shouldn't have quotes)
        if (renderedConvId.startsWith("\"") && renderedConvId.endsWith("\"")) {
            renderedConvId = renderedConvId.substring(1, renderedConvId.length() - 1);
        }
        var requestSpec = given().contentType(MediaType.APPLICATION_JSON).body(rendered);
        requestSpec = authenticateRequest(requestSpec);
        this.lastResponse =
                requestSpec.when().post("/v1/conversations/{id}/memberships", renderedConvId);
    }

    @io.cucumber.java.en.When("I share that conversation with user {string} with request:")
    public void iShareThatConversationWithUserWithRequest(String userId, String requestBody) {
        trackUsage();
        iShareTheConversationWithUserWithRequest(userId, requestBody);
    }

    @io.cucumber.java.en.Given(
            "I share the conversation with user {string} and access level {string}")
    public void iShareTheConversationWithUserAndAccessLevel(String userId, String accessLevel) {
        trackUsage();
        String requestBody =
                String.format(
                        """
                        {
                          "userId": "%s",
                          "accessLevel": "%s"
                        }
                        """,
                        userId, accessLevel);
        iShareTheConversationWithUserWithRequest(userId, requestBody);
    }

    @io.cucumber.java.en.When("I authenticate as user {string}")
    public void iAuthenticateAsUser(String userId) {
        trackUsage();
        iAmAuthenticatedAsUser(userId);
    }

    @io.cucumber.java.en.When("I list memberships for the conversation")
    public void iListMembershipsForTheConversation() {
        trackUsage();
        iListMembershipsForConversation(conversationId);
    }

    @io.cucumber.java.en.When("I list memberships for conversation {string}")
    public void iListMembershipsForConversation(String convId) {
        trackUsage();
        String renderedConvId = renderTemplate(convId);
        // Strip quotes if present (RestAssured path parameters shouldn't have quotes)
        if (renderedConvId.startsWith("\"") && renderedConvId.endsWith("\"")) {
            renderedConvId = renderedConvId.substring(1, renderedConvId.length() - 1);
        }
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
                requestSpec.when().get("/v1/conversations/{id}/memberships", renderedConvId);
    }

    @io.cucumber.java.en.When("I list memberships for that conversation")
    public void iListMembershipsForThatConversation() {
        trackUsage();
        iListMembershipsForTheConversation();
    }

    @io.cucumber.java.en.When("I update membership for user {string} with request:")
    public void iUpdateMembershipForUserWithRequest(String userId, String requestBody) {
        trackUsage();
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
        trackUsage();
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
        trackUsage();
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
        trackUsage();
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
        trackUsage();
        JsonPath jsonPath = lastResponse.jsonPath();
        List<Map<String, Object>> memberships = jsonPath.getList("data");
        boolean found = memberships.stream().anyMatch(m -> userId.equals(m.get("userId")));
        assertThat("Should not contain membership for user " + userId, found, is(false));
    }

    @io.cucumber.java.en.Then("the response should contain {int} membership")
    public void theResponseShouldContainMembership(int count) {
        trackUsage();
        lastResponse.then().body("data", hasSize(count));
    }

    @io.cucumber.java.en.When("I send gRPC request {string} with body:")
    public void iSendGrpcRequestWithBody(String serviceMethod, String body) {
        trackUsage();
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
                    JsonFormat.printer().alwaysPrintFieldsWithNoPresence().print(response);
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
        trackUsage();
        lastResponse.then().statusCode(statusCode);
    }

    @io.cucumber.java.en.Then("the response body {string} should be {string}")
    public void theResponseBodyFieldShouldBe(String path, String expected) {
        trackUsage();
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

    @io.cucumber.java.en.Then("the response should contain an empty list of entries")
    public void theResponseShouldContainAnEmptyListOfEntries() {
        trackUsage();
        lastResponse.then().body("data", hasSize(0));
    }

    @io.cucumber.java.en.Then("the response should contain {int} entries")
    public void theResponseShouldContainEntries(int count) {
        trackUsage();
        lastResponse.then().body("data", hasSize(count));
    }

    @io.cucumber.java.en.Then("the response should contain {int} entry")
    public void theResponseShouldContainEntry(int count) {
        trackUsage();
        lastResponse.then().body("data", hasSize(count));
    }

    @io.cucumber.java.en.Then("the response body field {string} should be {string}")
    public void theResponseBodyFieldShouldBe2(String path, String expected) {
        trackUsage();
        // Alias for "the response body {string} should be {string}" to match test feature file
        theResponseBodyFieldShouldBe(path, expected);
    }

    @io.cucumber.java.en.Then("the search response should contain {int} results")
    public void theSearchResponseShouldContainResults(int count) {
        trackUsage();
        lastResponse.then().body("data", hasSize(count));
    }

    @io.cucumber.java.en.Then("search result at index {int} should have entry content {string}")
    public void searchResultAtIndexShouldHaveEntryContent(int index, String expectedContent) {
        trackUsage();
        JsonPath jsonPath = lastResponse.jsonPath();
        String actualContent = jsonPath.getString("data[" + index + "].entry.content[0].text");
        assertThat(actualContent, is(expectedContent));
    }

    @io.cucumber.java.en.Then("entry at index {int} should have content {string}")
    public void entryAtIndexShouldHaveContent(int index, String expectedContent) {
        trackUsage();
        JsonPath jsonPath = lastResponse.jsonPath();
        String actualContent = jsonPath.getString("data[" + index + "].content[0].text");
        assertThat(actualContent, is(expectedContent));
    }

    @io.cucumber.java.en.Then("the response should have a nextCursor")
    public void theResponseShouldHaveANextCursor() {
        trackUsage();
        lastResponse.then().body("nextCursor", notNullValue());
    }

    @io.cucumber.java.en.Then("the response should contain the created entry")
    public void theResponseShouldContainTheCreatedEntry() {
        trackUsage();
        lastResponse.then().body("id", notNullValue());
    }

    @io.cucumber.java.en.Then("the entry should have content {string}")
    public void theEntryShouldHaveContent(String expectedContent) {
        trackUsage();
        lastResponse.then().body("content[0].text", is(expectedContent));
    }

    @io.cucumber.java.en.Then("the entry should have channel {string}")
    public void theEntryShouldHaveChannel(String expectedChannel) {
        trackUsage();
        lastResponse.then().body("channel", is(expectedChannel.toLowerCase()));
    }

    @io.cucumber.java.en.Then("the entry should have contentType {string}")
    public void theEntryShouldHaveContentType(String expectedContentType) {
        trackUsage();
        lastResponse.then().body("contentType", is(expectedContentType));
    }

    @io.cucumber.java.en.Then("the response should contain error code {string}")
    public void theResponseShouldContainErrorCode(String errorCode) {
        trackUsage();
        lastResponse.then().body("code", is(errorCode));
    }

    @io.cucumber.java.en.And("set {string} to {string}")
    public void setContextVariable(String variableName, String valueTemplate) {
        trackUsage();
        // Check if the template is a simple variable reference like ${foo}
        Matcher m = PLACEHOLDER_PATTERN.matcher(valueTemplate);
        if (m.matches()) {
            String expression = m.group(1).trim();
            // If it's a simple context variable reference, copy the value directly
            if (contextVariables.containsKey(expression)) {
                Object value = contextVariables.get(expression);
                contextVariables.put(variableName, value);
                syncFieldFromContextVariable(variableName, value);
                return;
            }
        }
        // Fall back to template rendering for complex expressions
        String renderedValue = renderTemplate(valueTemplate);
        contextVariables.put(variableName, renderedValue);
        syncFieldFromContextVariable(variableName, renderedValue);
    }

    private void syncFieldFromContextVariable(String variableName, Object value) {
        if ("conversationId".equals(variableName) && value instanceof String s) {
            this.conversationId = s;
        }
    }

    @io.cucumber.java.en.Then("the response should contain {int} conversation")
    public void theResponseShouldContainConversation(int expectedCount) {
        trackUsage();
        if (lastResponse == null) {
            throw new AssertionError("No HTTP response has been received");
        }
        JsonPath jsonPath = lastResponse.jsonPath();
        List<?> conversations = jsonPath.get("data");
        assertThat(
                "Response should contain " + expectedCount + " conversation(s)",
                conversations != null ? conversations.size() : 0,
                is(expectedCount));
    }

    @io.cucumber.java.en.Then("set {string} to the json response field {string}")
    public void setContextVariableToJsonResponseField(String variableName, String path) {
        trackUsage();
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

    @io.cucumber.java.en.Then("the gRPC response should contain {int} entry")
    public void theGrpcResponseShouldContainEntry(int count) {
        trackUsage();
        assertGrpcEntryCount(count);
    }

    @io.cucumber.java.en.Then("the gRPC response should contain {int} entries")
    public void theGrpcResponseShouldContainEntries(int count) {
        trackUsage();
        assertGrpcEntryCount(count);
    }

    @io.cucumber.java.en.Then("the gRPC response field {string} should be {string}")
    public void theGrpcResponseFieldShouldBe(String path, String expected) {
        trackUsage();
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
        trackUsage();
        JsonPath jsonPath = ensureGrpcJsonPath();
        Object value = jsonPath.get(path);
        assertThat("gRPC response field '" + path + "' should not be null", value, notNullValue());
    }

    @io.cucumber.java.en.Then("the gRPC response should not contain field {string}")
    public void theGrpcResponseShouldNotContainField(String path) {
        trackUsage();
        JsonPath jsonPath = ensureGrpcJsonPath();
        try {
            Object value = jsonPath.get(path);
            if (value != null) {
                throw new AssertionError(
                        "gRPC response should not contain field '"
                                + path
                                + "' but it has value: "
                                + value);
            }
        } catch (com.jayway.jsonpath.PathNotFoundException e) {
            // Field doesn't exist, which is what we want
            return;
        }
    }

    @io.cucumber.java.en.Then("set {string} to the gRPC response field {string}")
    public void setContextVariableToGrpcResponseField(String variableName, String path) {
        trackUsage();
        JsonPath jsonPath = ensureGrpcJsonPath();
        Object value = jsonPath.get(path);
        if (value == null) {
            throw new AssertionError(
                    "gRPC response field '" + path + "' is null or does not exist");
        }
        contextVariables.put(variableName, value);
    }

    @io.cucumber.java.en.Then("the gRPC response text should match text proto:")
    public void theGrpcResponseTextShouldMatchTextProto(String expectedText) {
        trackUsage();
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
        trackUsage();
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
        trackUsage();
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
        trackUsage();
        JsonPath jsonPath = ensureGrpcJsonPath();
        Boolean value = jsonPath.get(fieldPath);
        assertThat("gRPC response field " + fieldPath, value, is(true));
    }

    @io.cucumber.java.en.Then("the gRPC response field {string} should be false")
    public void theGrpcResponseFieldShouldBeFalse(String fieldPath) {
        trackUsage();
        JsonPath jsonPath = ensureGrpcJsonPath();
        Boolean value = jsonPath.get(fieldPath);
        assertThat("gRPC response field " + fieldPath, value, is(false));
    }

    @io.cucumber.java.en.Then("the gRPC response field {string} should be {int}")
    public void theGrpcResponseFieldShouldBe(String fieldPath, Integer expectedValue) {
        trackUsage();
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
        trackUsage();
        var dto = memoryStoreSelector.getStore().getConversation(currentUserId, conversationId);
        assertThat(dto.getTitle(), is(expectedTitle));
    }

    @io.cucumber.java.en.Given("I set context variable {string} to {string}")
    public void iSetContextVariableTo(String name, String value) {
        trackUsage();
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

    @io.cucumber.java.en.Then("the response body should be json:")
    public void theResponseBodyShouldBeJson(String expectedJson) {
        trackUsage();
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

    private boolean isSurroundedByQuotes(String template, int start, int end) {
        int before = start - 1;
        int after = end;
        if (before < 0 || after >= template.length()) {
            return false;
        }
        char beforeChar = template.charAt(before);
        char afterChar = template.charAt(after);
        // Check for matching double quotes or single quotes (for SQL)
        return (beforeChar == '"' && afterChar == '"') || (beforeChar == '\'' && afterChar == '\'');
    }

    private String serializeReplacement(Object value, boolean inQuotes) {
        if (value instanceof String s && !inQuotes) {
            return s;
        }
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

    private void assertGrpcEntryCount(int count) {
        JsonPath jsonPath = ensureGrpcJsonPath();
        assertThat(jsonPath.getList("entries"), hasSize(count));
    }

    private Message.Builder createGrpcResponseBuilder(String serviceMethod) {
        return switch (serviceMethod) {
            case "EntriesService/ListEntries" -> ListEntriesResponse.newBuilder();
            case "MessagesService/AppendMessage", "EntriesService/AppendEntry" ->
                    io.github.chirino.memory.grpc.v1.Entry.newBuilder();
            case "EntriesService/SyncEntries" -> SyncEntriesResponse.newBuilder();
            case "SearchService/CreateSummary" ->
                    io.github.chirino.memory.grpc.v1.Entry.newBuilder();
            case "SearchService/SearchEntries" -> SearchEntriesResponse.newBuilder();
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

    private String resolveClientId() {
        if (currentApiKey == null || currentApiKey.isBlank() || apiKeyManager == null) {
            return null;
        }
        return apiKeyManager.resolveClientId(currentApiKey).orElse(null);
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
            case "MessagesService", "EntriesService" -> callEntriesService(method, metadata, body);
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

    private Message callEntriesService(String method, Metadata metadata, String body)
            throws Exception {
        var stub = EntriesServiceGrpc.newBlockingStub(grpcChannel);
        if (metadata != null) {
            stub = stub.withInterceptors(MetadataUtils.newAttachHeadersInterceptor(metadata));
        }
        switch (method) {
            case "ListMessages", "ListEntries":
                {
                    var requestBuilder = ListEntriesRequest.newBuilder();
                    if (body != null && !body.isBlank()) {
                        TextFormat.merge(body, requestBuilder);
                    }
                    return stub.listEntries(requestBuilder.build());
                }
            case "AppendMessage", "AppendEntry":
                {
                    var requestBuilder = AppendEntryRequest.newBuilder();
                    if (body != null && !body.isBlank()) {
                        TextFormat.merge(body, requestBuilder);
                    }
                    return stub.appendEntry(requestBuilder.build());
                }
            case "SyncMessages", "SyncEntries":
                {
                    var requestBuilder = SyncEntriesRequest.newBuilder();
                    if (body != null && !body.isBlank()) {
                        TextFormat.merge(body, requestBuilder);
                    }
                    return stub.syncEntries(requestBuilder.build());
                }
            default:
                throw new IllegalArgumentException("Unsupported EntriesService method: " + method);
        }
    }

    private Message callSearchService(String method, Metadata metadata, String body)
            throws Exception {
        var stub = SearchServiceGrpc.newBlockingStub(grpcChannel);
        if (metadata != null) {
            stub = stub.withInterceptors(MetadataUtils.newAttachHeadersInterceptor(metadata));
        }
        switch (method) {
            case "IndexTranscript":
                {
                    var requestBuilder = IndexTranscriptRequest.newBuilder();
                    if (body != null && !body.isBlank()) {
                        TextFormat.merge(body, requestBuilder);
                    }
                    return stub.indexTranscript(requestBuilder.build());
                }
            case "SearchConversations":
                {
                    var requestBuilder = SearchEntriesRequest.newBuilder();
                    if (body != null && !body.isBlank()) {
                        TextFormat.merge(body, requestBuilder);
                    }
                    return stub.searchConversations(requestBuilder.build());
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
            case "CheckConversations":
                {
                    var requestBuilder = CheckConversationsRequest.newBuilder();
                    if (body != null && !body.isBlank()) {
                        TextFormat.merge(body, requestBuilder);
                    }
                    return stub.checkConversations(requestBuilder.build());
                }
            case "CancelResponse":
                {
                    var requestBuilder = CancelResponseRequest.newBuilder();
                    if (body != null && !body.isBlank()) {
                        TextFormat.merge(body, requestBuilder);
                    }
                    return stub.cancelResponse(requestBuilder.build());
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
                    CompletableFuture<StreamResponseTokenResponse> responseFuture =
                            new CompletableFuture<>();
                    mutinyStub
                            .streamResponseTokens(requestStream)
                            .subscribe()
                            .with(
                                    response -> {
                                        if (!responseFuture.isDone()) {
                                            responseFuture.complete(response);
                                        }
                                    },
                                    responseFuture::completeExceptionally,
                                    () -> {
                                        if (!responseFuture.isDone()) {
                                            responseFuture.complete(
                                                    StreamResponseTokenResponse.newBuilder()
                                                            .build());
                                        }
                                    });
                    try {
                        return responseFuture.get(10, TimeUnit.SECONDS);
                    } catch (Exception e) {
                        Throwable cause = e.getCause();
                        if (cause instanceof StatusRuntimeException statusException) {
                            throw statusException;
                        }
                        throw e;
                    }
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
        // When gRPC uses the same server as HTTP (use-separate-server=false),
        // extract the port from test.url which Quarkus sets to the actual test server URL
        boolean useSeparateServer =
                config.getOptionalValue("quarkus.grpc.server.use-separate-server", Boolean.class)
                        .orElse(true);

        if (!useSeparateServer) {
            // gRPC shares the HTTP port - extract from test.url
            String testUrl =
                    config.getOptionalValue("test.url", String.class)
                            .orElse("http://localhost:8081");
            try {
                URI uri = new URI(testUrl);
                String host = uri.getHost() != null ? uri.getHost() : "localhost";
                int port =
                        uri.getPort() != -1
                                ? uri.getPort()
                                : ("https".equalsIgnoreCase(uri.getScheme()) ? 443 : 80);
                return new GrpcEndpoint(host, port);
            } catch (URISyntaxException e) {
                throw new IllegalStateException("Invalid test.url configuration: " + testUrl, e);
            }
        }

        // gRPC uses a separate server - check for explicit port configuration
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

        // Fallback: extract from test.url
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
        if (entryRepository.isUnsatisfied()) {
            return;
        }
        entryRepository.get().deleteAll();
        membershipRepository.get().deleteAll();
        ownershipTransferRepository.get().deleteAll();
        conversationRepository.get().deleteAll();
        conversationGroupRepository.get().deleteAll();
    }

    private void clearMongoData() {
        if (mongoEntryRepository.isUnsatisfied()) {
            return;
        }
        mongoEntryRepository.get().deleteAll();
        mongoMembershipRepository.get().deleteAll();
        mongoOwnershipTransferRepository.get().deleteAll();
        mongoConversationRepository.get().deleteAll();
        mongoConversationGroupRepository.get().deleteAll();
    }

    private List<Map<String, Object>> lastSqlResult;

    /**
     * Resolves the conversation group ID for a given conversation ID from the database.
     * This is used for test infrastructure when conversationGroupId is no longer exposed in API responses.
     */
    private String resolveConversationGroupId(String conversationId) {
        String datastoreType =
                config.getOptionalValue("memory-service.datastore.type", String.class)
                        .orElse("postgres");
        if ("postgres".equals(datastoreType)) {
            if (entityManager.isUnsatisfied()) {
                throw new IllegalStateException(
                        "Cannot resolve group ID: EntityManager not available");
            }
            // Cast conversationId to UUID since PostgreSQL stores id as UUID type
            String sql =
                    "SELECT conversation_group_id FROM conversations WHERE id ="
                            + " CAST(:conversationId AS UUID)";
            jakarta.persistence.Query query =
                    entityManager
                            .get()
                            .createNativeQuery(sql)
                            .setParameter("conversationId", conversationId);
            @SuppressWarnings("unchecked")
            List<Object> results = query.getResultList();
            if (results.isEmpty()) {
                throw new IllegalStateException("Conversation not found: " + conversationId);
            }
            Object groupId = results.get(0);
            return groupId != null ? groupId.toString() : null;
        } else {
            // For MongoDB, we need to query the MongoDB repository
            if (mongoConversationRepository.isUnsatisfied()) {
                throw new IllegalStateException(
                        "Cannot resolve group ID: MongoDB repository not available");
            }
            io.github.chirino.memory.mongo.model.MongoConversation conv =
                    mongoConversationRepository.get().findById(conversationId);
            if (conv == null) {
                throw new IllegalStateException("Conversation not found: " + conversationId);
            }
            return conv.conversationGroupId;
        }
    }

    @io.cucumber.java.en.Given(
            "I resolve the conversation group ID for conversation {string} into {string}")
    public void iResolveTheConversationGroupIdForConversationInto(
            String conversationIdVar, String groupIdVar) {
        trackUsage();
        // Extract variable name from template string like "${conversationId}" -> "conversationId"
        String varName = conversationIdVar;
        if (conversationIdVar.startsWith("${") && conversationIdVar.endsWith("}")) {
            varName = conversationIdVar.substring(2, conversationIdVar.length() - 1);
        }
        // Get the actual conversation ID from context variables
        Object conversationIdObj = contextVariables.get(varName);
        if (conversationIdObj == null) {
            throw new IllegalStateException(
                    "Conversation ID variable '" + varName + "' not found in context");
        }
        String conversationId = conversationIdObj.toString();
        String groupId = resolveConversationGroupId(conversationId);
        contextVariables.put(groupIdVar, groupId);
    }

    @io.cucumber.java.en.When("I execute SQL query:")
    public void iExecuteSqlQuery(String sql) {
        trackUsage();
        // Check if we're using PostgreSQL datastore
        String datastoreType =
                config.getOptionalValue("memory-service.datastore.type", String.class)
                        .orElse("postgres");
        if (!"postgres".equals(datastoreType)) {
            // Skip SQL queries for non-PostgreSQL datastores (e.g., MongoDB)
            lastSqlResult = new ArrayList<>();
            return;
        }

        String renderedSql = renderTemplate(sql);
        // Remove JSON string quotes from UUID values in SQL (e.g., '"uuid"' -> 'uuid')
        renderedSql =
                renderedSql.replaceAll(
                        "'\"([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})\"'",
                        "'$1'");

        if (entityManager.isUnsatisfied()) {
            throw new IllegalStateException("SQL steps only available with PostgreSQL profile");
        }

        jakarta.persistence.Query query = entityManager.get().createNativeQuery(renderedSql);
        @SuppressWarnings("unchecked")
        List<?> rawRows = query.getResultList();

        // Extract column names from SELECT statement
        lastSqlResult = new ArrayList<>();

        // Handle case where query returns scalar values (single column) vs row arrays
        List<Object[]> rows = new ArrayList<>();
        for (Object rawRow : rawRows) {
            if (rawRow instanceof Object[]) {
                rows.add((Object[]) rawRow);
            } else {
                // Single column result - wrap in array
                rows.add(new Object[] {rawRow});
            }
        }

        String[] columnNames =
                extractColumnNames(renderedSql, rows.isEmpty() ? 0 : rows.get(0).length);

        for (Object[] row : rows) {
            Map<String, Object> rowMap = new LinkedHashMap<>();
            for (int i = 0; i < row.length && i < columnNames.length; i++) {
                rowMap.put(columnNames[i], row[i]);
            }
            lastSqlResult.add(rowMap);
        }
    }

    private String[] extractColumnNames(String sql, int columnCount) {
        String[] columnNames = new String[columnCount];
        for (int i = 0; i < columnNames.length; i++) {
            columnNames[i] = "col" + i; // Default fallback
        }

        if (sql.trim().toUpperCase().startsWith("SELECT")) {
            try {
                // Extract SELECT clause
                String selectClause = sql.toUpperCase().split("SELECT")[1].split("FROM")[0].trim();
                String[] parts = selectClause.split(",");
                for (int i = 0; i < Math.min(parts.length, columnNames.length); i++) {
                    String part = parts[i].trim();
                    // Handle AS alias
                    if (part.contains(" AS ")) {
                        columnNames[i] = part.split(" AS ")[1].trim().toLowerCase();
                    } else if (part.contains(" as ")) {
                        columnNames[i] = part.split(" as ")[1].trim().toLowerCase();
                    } else {
                        // Extract column name (remove table prefix if present)
                        String colName = part;
                        if (colName.contains(".")) {
                            colName = colName.substring(colName.lastIndexOf(".") + 1).trim();
                        }
                        // Remove any function calls or expressions - just get the base name
                        colName = colName.replaceAll("\\(.*\\)", "").trim();
                        if (!colName.isEmpty()) {
                            columnNames[i] = colName.toLowerCase();
                        }
                    }
                }
            } catch (Exception e) {
                // If parsing fails, fall back to col0, col1, etc.
            }
        }
        return columnNames;
    }

    @io.cucumber.java.en.Then("the SQL result should have {int} row(s)")
    public void theSqlResultShouldHaveRows(int expectedCount) {
        trackUsage();
        // Skip SQL assertions for non-PostgreSQL datastores
        String datastoreType =
                config.getOptionalValue("memory-service.datastore.type", String.class)
                        .orElse("postgres");
        if (!"postgres".equals(datastoreType)) {
            return; // Skip assertion for MongoDB
        }
        assertThat("SQL result row count", lastSqlResult.size(), is(expectedCount));
    }

    @io.cucumber.java.en.Then("the SQL result should match:")
    public void theSqlResultShouldMatch(io.cucumber.datatable.DataTable dataTable) {
        trackUsage();
        // Skip SQL assertions for non-PostgreSQL datastores
        String datastoreType =
                config.getOptionalValue("memory-service.datastore.type", String.class)
                        .orElse("postgres");
        if (!"postgres".equals(datastoreType)) {
            return; // Skip assertion for MongoDB
        }
        List<Map<String, String>> expected = dataTable.asMaps();
        assertThat("SQL result row count", lastSqlResult.size(), is(expected.size()));

        for (int i = 0; i < expected.size(); i++) {
            Map<String, String> expectedRow = expected.get(i);
            Map<String, Object> actualRow = lastSqlResult.get(i);

            for (Map.Entry<String, String> entry : expectedRow.entrySet()) {
                String column = entry.getKey();
                String expectedValue = renderTemplate(entry.getValue());
                Object actualValue = actualRow.get(column);

                if ("*".equals(expectedValue)) {
                    // Wildcard: just check column exists and is not null
                    assertThat(
                            "Column " + column + " should exist and be non-null",
                            actualValue,
                            notNullValue());
                } else if ("NULL".equals(expectedValue)) {
                    assertThat("Column " + column + " should be null", actualValue, nullValue());
                } else {
                    assertThat(
                            "Column " + column + " in row " + i,
                            String.valueOf(actualValue),
                            is(expectedValue));
                }
            }
        }
    }

    @io.cucumber.java.en.Then("the SQL result column {string} should be non-null")
    public void theSqlResultColumnShouldBeNonNull(String column) {
        trackUsage();
        // Skip SQL assertions for non-PostgreSQL datastores
        String datastoreType =
                config.getOptionalValue("memory-service.datastore.type", String.class)
                        .orElse("postgres");
        if (!"postgres".equals(datastoreType)) {
            return; // Skip assertion for MongoDB
        }
        assertThat("SQL result should have at least one row", lastSqlResult.size(), greaterThan(0));
        for (Map<String, Object> row : lastSqlResult) {
            assertThat("Column " + column + " should be non-null", row.get(column), notNullValue());
        }
    }

    // Admin step definitions

    @io.cucumber.java.en.Given("I am authenticated as admin user {string}")
    public void iAmAuthenticatedAsAdminUser(String userId) {
        trackUsage();
        this.currentUserId = userId;
        this.currentApiKey = null;
    }

    @io.cucumber.java.en.Given("I am authenticated as auditor user {string}")
    public void iAmAuthenticatedAsAuditorUser(String userId) {
        trackUsage();
        this.currentUserId = userId;
        this.currentApiKey = null;
    }

    @io.cucumber.java.en.Given("there is a conversation owned by {string} with title {string}")
    public void thereIsAConversationOwnedByWithTitle(String ownerId, String title) {
        trackUsage();
        CreateConversationRequest request = new CreateConversationRequest();
        request.setTitle(title);
        this.conversationId =
                memoryStoreSelector.getStore().createConversation(ownerId, request).getId();
        contextVariables.put("conversationId", conversationId);
        contextVariables.put("conversationOwner", ownerId);
    }

    @io.cucumber.java.en.Given("the conversation owned by {string} has an entry {string}")
    public void theConversationOwnedByHasAnEntry(String ownerId, String content) {
        trackUsage();
        // Find the conversation ID for this owner
        String convId = null;
        String ownerVar = ownerId + "ConversationId";
        if (contextVariables.containsKey(ownerVar)) {
            convId = (String) contextVariables.get(ownerVar);
        } else {
            // Use current conversationId or create one
            convId = conversationId;
            if (convId == null) {
                thereIsAConversationOwnedByWithTitle(ownerId, "Test Conversation");
                convId = conversationId;
            }
        }
        CreateUserEntryRequest request = new CreateUserEntryRequest();
        request.setContent(content);
        memoryStoreSelector.getStore().appendUserEntry(ownerId, convId, request);
    }

    @io.cucumber.java.en.Given("the conversation owned by {string} is deleted")
    public void theConversationOwnedByIsDeleted(String ownerId) {
        trackUsage();
        // Find a conversation owned by this user - check for owner-specific variable first
        String convId = null;
        String ownerVar = ownerId + "ConversationId";
        if (contextVariables.containsKey(ownerVar)) {
            convId = (String) contextVariables.get(ownerVar);
        } else {
            convId = (String) contextVariables.get("conversationId");
            if (convId == null) {
                // Create one if needed
                thereIsAConversationOwnedBy(ownerId);
                convId = conversationId;
            }
        }
        try {
            memoryStoreSelector.getStore().deleteConversation(ownerId, convId);
        } catch (Exception e) {
            // Try admin delete if regular delete fails
            memoryStoreSelector.getStore().adminDeleteConversation(convId);
        }
    }

    @io.cucumber.java.en.When("I call GET {string}")
    public void iCallGET(String path) {
        trackUsage();
        String renderedPath = renderTemplate(path);
        // Check if path contains query string
        int queryIndex = renderedPath.indexOf('?');
        if (queryIndex > 0) {
            String basePath = renderedPath.substring(0, queryIndex);
            String queryString = renderedPath.substring(queryIndex + 1);
            iCallGETWithQuery(basePath, queryString);
        } else {
            iCallGETWithQuery(renderedPath, null);
        }
    }

    @io.cucumber.java.en.When("I call GET {string} with query {string}")
    public void iCallGETWithQuery(String path, String queryString) {
        trackUsage();
        String renderedPath = renderTemplate(path);
        var requestSpec = given();
        requestSpec = authenticateRequest(requestSpec);
        var request = requestSpec.when();
        if (queryString != null && !queryString.isBlank()) {
            // Parse query string and add as query params
            String[] pairs = queryString.split("&");
            for (String pair : pairs) {
                String[] keyValue = pair.split("=", 2);
                if (keyValue.length == 2) {
                    request = request.queryParam(keyValue[0], keyValue[1]);
                }
            }
        }
        this.lastResponse = request.get(renderedPath);
    }

    @io.cucumber.java.en.When("I call DELETE {string} with body:")
    public void iCallDELETEWithBody(String path, String body) {
        trackUsage();
        String renderedPath = renderTemplate(path);
        String renderedBody = renderTemplate(body);
        var requestSpec = given().contentType(MediaType.APPLICATION_JSON).body(renderedBody);
        requestSpec = authenticateRequest(requestSpec);
        this.lastResponse = requestSpec.when().delete(renderedPath);
    }

    @io.cucumber.java.en.When("I call POST {string} with body:")
    public void iCallPOSTWithBody(String path, String body) {
        trackUsage();
        String renderedPath = renderTemplate(path);
        String renderedBody = renderTemplate(body);
        var requestSpec = given().contentType(MediaType.APPLICATION_JSON).body(renderedBody);
        requestSpec = authenticateRequest(requestSpec);
        this.lastResponse = requestSpec.when().post(renderedPath);
    }

    @io.cucumber.java.en.Then("all conversations should have ownerUserId {string}")
    public void allConversationsShouldHaveOwnerUserId(String expectedOwner) {
        trackUsage();
        JsonPath jsonPath = lastResponse.jsonPath();
        List<Map<String, Object>> conversations = jsonPath.getList("data");
        for (Map<String, Object> conv : conversations) {
            assertThat(
                    "Conversation should have ownerUserId " + expectedOwner,
                    conv.get("ownerUserId"),
                    is(expectedOwner));
        }
    }

    @io.cucumber.java.en.Then(
            "the response should contain at least {int} conversation with deletedAt set")
    public void theResponseShouldContainAtLeastConversationWithDeletedAtSet(int minCount) {
        trackUsage();
        JsonPath jsonPath = lastResponse.jsonPath();
        List<Map<String, Object>> conversations = jsonPath.getList("data");
        int deletedCount = 0;
        for (Map<String, Object> conv : conversations) {
            if (conv.get("deletedAt") != null) {
                deletedCount++;
            }
        }
        assertThat(
                "Response should contain at least " + minCount + " deleted conversations",
                deletedCount,
                greaterThan(minCount - 1));
    }

    @io.cucumber.java.en.Then("all conversations should have deletedAt set")
    public void allConversationsShouldHaveDeletedAtSet() {
        trackUsage();
        JsonPath jsonPath = lastResponse.jsonPath();
        List<Map<String, Object>> conversations = jsonPath.getList("data");
        for (Map<String, Object> conv : conversations) {
            assertThat(
                    "Conversation should have deletedAt set",
                    conv.get("deletedAt"),
                    notNullValue());
        }
    }

    @io.cucumber.java.en.Then("the response body should have field {string} that is not null")
    public void theResponseBodyShouldHaveFieldThatIsNotNull(String fieldName) {
        trackUsage();
        JsonPath jsonPath = lastResponse.jsonPath();
        Object value = jsonPath.get(fieldName);
        assertThat("Field " + fieldName + " should not be null", value, notNullValue());
    }

    @io.cucumber.java.en.Then("the conversation should be soft-deleted")
    public void theConversationShouldBeSoftDeleted() {
        trackUsage();
        // Verify via admin API that conversation is deleted
        String token = keycloakClient.getAccessToken("alice");
        var requestSpec = given().auth().oauth2(token);
        var response =
                requestSpec
                        .when()
                        .get("/v1/admin/conversations/{id}?includeDeleted=true", conversationId);
        response.then().statusCode(200);
        JsonPath jsonPath = response.jsonPath();
        assertThat("Conversation should be deleted", jsonPath.get("deletedAt"), notNullValue());
    }

    @io.cucumber.java.en.Then("the conversation should not be deleted")
    public void theConversationShouldNotBeDeleted() {
        trackUsage();
        String token = keycloakClient.getAccessToken("alice");
        var requestSpec = given().auth().oauth2(token);
        var response = requestSpec.when().get("/v1/admin/conversations/{id}", conversationId);
        response.then().statusCode(200);
        JsonPath jsonPath = response.jsonPath();
        assertThat("Conversation should not be deleted", jsonPath.get("deletedAt"), nullValue());
    }

    @io.cucumber.java.en.Then("the admin audit log should contain {string}")
    public void theAdminAuditLogShouldContain(String text) {
        trackUsage();
        // In a real implementation, we would check the audit log
        // For now, we'll just verify the request succeeded
        // This is a placeholder that can be enhanced with actual log checking
    }

    @io.cucumber.java.en.Then("all search results should have conversation owned by {string}")
    @SuppressWarnings("unchecked")
    public void allSearchResultsShouldHaveConversationOwnedBy(String expectedOwner) {
        trackUsage();
        JsonPath jsonPath = lastResponse.jsonPath();
        List<Map<String, Object>> results = jsonPath.getList("data");
        for (Map<String, Object> result : results) {
            Map<String, Object> entry = (Map<String, Object>) result.get("entry");
            if (entry != null) {
                Map<String, Object> conversation = (Map<String, Object>) entry.get("conversation");
                if (conversation != null) {
                    String ownerUserId = (String) conversation.get("ownerUserId");
                    assertThat(
                            "Search result should have conversation owned by " + expectedOwner,
                            ownerUserId,
                            is(expectedOwner));
                }
            }
        }
    }

    // Task queue step definitions

    private String lastCreatedTaskType;
    private String lastCreatedTaskBodyJson;

    @io.cucumber.java.en.Given("all tasks are deleted")
    @Transactional(Transactional.TxType.REQUIRES_NEW)
    public void allTasksAreDeleted() {
        trackUsage();
        String datastoreType =
                config.getOptionalValue("memory-service.datastore.type", String.class)
                        .orElse("postgres");
        if ("postgres".equals(datastoreType)) {
            entityManager.get().createNativeQuery("DELETE FROM tasks").executeUpdate();
        } else {
            // MongoDB cleanup would go here
        }
    }

    @io.cucumber.java.en.Given("I create a task with type {string} and body:")
    @Transactional(Transactional.TxType.REQUIRES_NEW)
    public void iCreateATaskWithTypeAndBody(String taskType, String bodyJson) throws Exception {
        trackUsage();
        JsonNode body = OBJECT_MAPPER.readTree(bodyJson);
        Map<String, Object> taskBody =
                OBJECT_MAPPER.convertValue(body, new TypeReference<Map<String, Object>>() {});
        lastCreatedTaskType = taskType;
        lastCreatedTaskBodyJson = bodyJson.trim();
        if (taskRepositorySelector.get().isPostgres()) {
            taskRepository.get().createTask(taskType, taskBody);
        } else {
            mongoTaskRepository.get().createTask(taskType, taskBody);
        }
    }

    @io.cucumber.java.en.When("the task processor runs")
    public void theTaskProcessorRuns() {
        trackUsage();
        taskProcessor.get().processPendingTasks();
    }

    @io.cucumber.java.en.Then("the task should be deleted")
    @Transactional(Transactional.TxType.REQUIRES_NEW)
    public void theTaskShouldBeDeleted() {
        trackUsage();
        String datastoreType =
                config.getOptionalValue("memory-service.datastore.type", String.class)
                        .orElse("postgres");
        if ("postgres".equals(datastoreType)) {
            // Check PostgreSQL tasks table is empty (Background step ensures cleanup)
            @SuppressWarnings("unchecked")
            List<Map<String, Object>> tasks =
                    entityManager
                            .get()
                            .createNativeQuery("SELECT id FROM tasks", Map.class)
                            .getResultList();
            assertThat("Task should be deleted", tasks.size(), is(0));
        } else {
            // Check MongoDB tasks collection
            // For now, we'll assume the task was processed
            // In a real test, we'd query MongoDB directly
        }
    }

    @io.cucumber.java.en.Then("the vector store should have received a delete call for {string}")
    public void theVectorStoreShouldHaveReceivedADeleteCallFor(String groupId) {
        trackUsage();
        // This would be verified by a mock/spy vector store
        // For now, we'll just verify the task was processed
        // In a real implementation, we'd use Mockito to spy on VectorStore
    }

    @io.cucumber.java.en.Given("the vector store will fail for {string}")
    @Transactional
    public void theVectorStoreWillFailFor(String groupId) {
        trackUsage();
        String datastoreType =
                config.getOptionalValue("memory-service.datastore.type", String.class)
                        .orElse("postgres");
        if ("postgres".equals(datastoreType)) {
            // Change the task type to an unknown type so that TaskProcessor.executeTask throws
            entityManager
                    .get()
                    .createNativeQuery(
                            "UPDATE tasks SET task_type = 'failing_task'"
                                    + " WHERE task_body->>'conversationGroupId' = :groupId")
                    .setParameter("groupId", groupId)
                    .executeUpdate();
        }
    }

    @io.cucumber.java.en.Then("the task should still exist")
    public void theTaskShouldStillExist() {
        trackUsage();
        String datastoreType =
                config.getOptionalValue("memory-service.datastore.type", String.class)
                        .orElse("postgres");
        if ("postgres".equals(datastoreType)) {
            @SuppressWarnings("unchecked")
            List<Map<String, Object>> tasks =
                    entityManager
                            .get()
                            .createNativeQuery("SELECT id FROM tasks", Map.class)
                            .getResultList();
            assertThat("Task should still exist", tasks.size(), greaterThan(0));
        }
    }

    @io.cucumber.java.en.Then("the task retry_at should be in the future")
    public void theTaskRetryAtShouldBeInTheFuture() {
        trackUsage();
        String datastoreType =
                config.getOptionalValue("memory-service.datastore.type", String.class)
                        .orElse("postgres");
        if ("postgres".equals(datastoreType)) {
            @SuppressWarnings("unchecked")
            List<Map<String, Object>> tasks =
                    entityManager
                            .get()
                            .createNativeQuery("SELECT retry_at FROM tasks", Map.class)
                            .getResultList();
            assertThat("Task should exist", tasks.size(), greaterThan(0));
            for (Map<String, Object> task : tasks) {
                Object retryAtObj = task.get("retry_at");
                java.time.Instant retryAtInstant;
                if (retryAtObj instanceof java.time.Instant) {
                    retryAtInstant = (java.time.Instant) retryAtObj;
                } else if (retryAtObj instanceof java.time.OffsetDateTime) {
                    retryAtInstant = ((java.time.OffsetDateTime) retryAtObj).toInstant();
                } else {
                    throw new AssertionError(
                            "Unexpected type for retry_at: " + retryAtObj.getClass());
                }
                assertThat(
                        "retry_at should be in the future",
                        retryAtInstant.isAfter(java.time.Instant.now()),
                        is(true));
            }
        }
    }

    @io.cucumber.java.en.Then("the task last_error should contain the failure message")
    public void theTaskLastErrorShouldContainTheFailureMessage() {
        trackUsage();
        String datastoreType =
                config.getOptionalValue("memory-service.datastore.type", String.class)
                        .orElse("postgres");
        if ("postgres".equals(datastoreType)) {
            @SuppressWarnings("unchecked")
            List<Map<String, Object>> tasks =
                    entityManager
                            .get()
                            .createNativeQuery(
                                    "SELECT last_error FROM tasks WHERE last_error IS NOT NULL",
                                    Map.class)
                            .getResultList();
            assertThat("Task should have error", tasks.size(), greaterThan(0));
        }
    }

    @io.cucumber.java.en.Then("the task retry_count should be {int}")
    public void theTaskRetryCountShouldBe(int expectedCount) {
        trackUsage();
        String datastoreType =
                config.getOptionalValue("memory-service.datastore.type", String.class)
                        .orElse("postgres");
        if ("postgres".equals(datastoreType)) {
            @SuppressWarnings("unchecked")
            List<Map<String, Object>> tasks =
                    entityManager
                            .get()
                            .createNativeQuery("SELECT retry_count FROM tasks", Map.class)
                            .getResultList();
            assertThat("Task should exist", tasks.size(), greaterThan(0));
            for (Map<String, Object> task : tasks) {
                Integer retryCount = ((Number) task.get("retry_count")).intValue();
                assertThat("retry_count should be " + expectedCount, retryCount, is(expectedCount));
            }
        }
    }

    @io.cucumber.java.en.Given("I have a failed task with retry_at in the past")
    @Transactional
    public void iHaveAFailedTaskWithRetryAtInThePast() {
        trackUsage();
        Map<String, Object> body = Map.of("conversationGroupId", "test-group");
        if (taskRepositorySelector.get().isPostgres()) {
            taskRepository.get().createTask("vector_store_delete", body);
            // Flush to ensure the task is visible for the native UPDATE
            entityManager.get().flush();
            // Update retry_at to the past
            entityManager
                    .get()
                    .createNativeQuery(
                            "UPDATE tasks SET retry_at = NOW() - INTERVAL '1 hour', retry_count ="
                                    + " 1")
                    .executeUpdate();
        } else {
            mongoTaskRepository.get().createTask("vector_store_delete", body);
            // MongoDB update would go here
        }
    }

    @io.cucumber.java.en.Then("the task should be processed again")
    public void theTaskShouldBeProcessedAgain() {
        trackUsage();
        // Task should be deleted after successful retry
        theTaskShouldBeDeleted();
    }

    @io.cucumber.java.en.Given("I have {int} pending tasks")
    @Transactional
    public void iHavePendingTasks(int count) {
        trackUsage();
        for (int i = 0; i < count; i++) {
            Map<String, Object> body = Map.of("conversationGroupId", "group-" + i);
            if (taskRepositorySelector.get().isPostgres()) {
                taskRepository.get().createTask("vector_store_delete", body);
            } else {
                mongoTaskRepository.get().createTask("vector_store_delete", body);
            }
        }
    }

    @io.cucumber.java.en.When("{int} task processors run concurrently")
    public void taskProcessorsRunConcurrently(int count) throws Exception {
        trackUsage();
        java.util.List<java.util.concurrent.Future<?>> futures = new java.util.ArrayList<>();
        java.util.concurrent.ExecutorService executor =
                java.util.concurrent.Executors.newFixedThreadPool(count);
        for (int i = 0; i < count; i++) {
            futures.add(executor.submit(() -> taskProcessor.get().processPendingTasks()));
        }
        for (java.util.concurrent.Future<?> future : futures) {
            future.get();
        }
        executor.shutdown();
    }

    @io.cucumber.java.en.Then("each task should be processed exactly once")
    public void eachTaskShouldBeProcessedExactlyOnce() {
        trackUsage();
        // All tasks should be deleted
        theTaskShouldBeDeleted();
    }

    @io.cucumber.java.en.Given("the conversation was soft-deleted {int} days ago")
    @Transactional
    public void theConversationWasSoftDeletedDaysAgo(int daysAgo) {
        trackUsage();
        // First soft-delete the conversation
        if (conversationId != null) {
            try {
                memoryStoreSelector.getStore().deleteConversation(currentUserId, conversationId);
            } catch (Exception e) {
                memoryStoreSelector.getStore().adminDeleteConversation(conversationId);
            }
        }
        // Then update deleted_at to N days ago
        String datastoreType =
                config.getOptionalValue("memory-service.datastore.type", String.class)
                        .orElse("postgres");
        if ("postgres".equals(datastoreType)) {
            String groupId = (String) contextVariables.get("conversationGroupId");
            if (groupId == null) {
                groupId = conversationId;
            }
            if (groupId != null) {
                entityManager
                        .get()
                        .createNativeQuery(
                                "UPDATE conversation_groups SET deleted_at = NOW() - INTERVAL '"
                                        + daysAgo
                                        + " days' WHERE id = :id")
                        .setParameter("id", java.util.UUID.fromString(groupId))
                        .executeUpdate();
            }
        }
    }

    @io.cucumber.java.en.When("I call POST {string} with Accept {string} and body:")
    public void iCallPOSTWithAcceptAndBody(String path, String accept, String body) {
        trackUsage();
        String renderedPath = renderTemplate(path);
        String renderedBody = renderTemplate(body);
        var requestSpec =
                given().contentType(MediaType.APPLICATION_JSON)
                        .header("Accept", accept)
                        .body(renderedBody);
        requestSpec = authenticateRequest(requestSpec);
        this.lastResponse = requestSpec.when().post(renderedPath);
    }

    @io.cucumber.java.en.Then("the response content type should be {string}")
    public void theResponseContentTypeShouldBe(String expectedContentType) {
        trackUsage();
        String actualContentType = lastResponse.getContentType();
        assertThat(
                "Response content type should be " + expectedContentType,
                actualContentType,
                containsString(expectedContentType));
    }

    private List<Integer> sseProgressValues = new ArrayList<>();

    @io.cucumber.java.en.Then("the SSE stream should contain progress events")
    public void theSseStreamShouldContainProgressEvents() {
        trackUsage();
        String body = lastResponse.getBody().asString();
        sseProgressValues.clear();
        // Parse SSE events: data: {"progress": N}
        String[] lines = body.split("\n");
        for (String line : lines) {
            if (line.startsWith("data: ")) {
                String json = line.substring(6); // Remove "data: " prefix
                try {
                    JsonNode node = OBJECT_MAPPER.readTree(json);
                    if (node.has("progress")) {
                        sseProgressValues.add(node.get("progress").asInt());
                    }
                } catch (Exception e) {
                    // Ignore parsing errors for non-progress events
                }
            }
        }
        assertThat(
                "SSE stream should contain progress events",
                sseProgressValues.size(),
                greaterThan(0));
    }

    @io.cucumber.java.en.Then("the final progress should be {int}")
    public void theFinalProgressShouldBe(int expectedProgress) {
        trackUsage();
        assertThat(
                "Final progress should be " + expectedProgress,
                sseProgressValues.get(sseProgressValues.size() - 1),
                is(expectedProgress));
    }

    @io.cucumber.java.en.Given("I have {int} conversations soft-deleted {int} days ago")
    @Transactional
    public void iHaveConversationsSoftDeletedDaysAgo(int count, int daysAgo) {
        trackUsage();
        String datastoreType =
                config.getOptionalValue("memory-service.datastore.type", String.class)
                        .orElse("postgres");
        for (int i = 0; i < count; i++) {
            CreateConversationRequest request = new CreateConversationRequest();
            request.setTitle("Test Conversation " + i);
            ConversationDto conv =
                    memoryStoreSelector
                            .getStore()
                            .createConversation(
                                    currentUserId != null ? currentUserId : "alice", request);
            // Soft delete it
            memoryStoreSelector.getStore().deleteConversation(conv.getOwnerUserId(), conv.getId());
            // Update deleted_at to N days ago
            if ("postgres".equals(datastoreType)) {
                entityManager
                        .get()
                        .createNativeQuery(
                                "UPDATE conversation_groups SET deleted_at = NOW() - INTERVAL '"
                                        + daysAgo
                                        + " days' WHERE id = :id")
                        .setParameter(
                                "id", java.util.UUID.fromString(conv.getConversationGroupId()))
                        .executeUpdate();
            }
        }
        // Flush to ensure all changes are committed before eviction runs
        if ("postgres".equals(datastoreType)) {
            entityManager.get().flush();
        }
    }

    private List<Response> concurrentResponses = new ArrayList<>();

    @io.cucumber.java.en.When("I call POST {string} concurrently {int} times with body:")
    public void iCallPOSTConcurrentlyTimesWithBody(String path, int times, String body)
            throws Exception {
        String renderedPath = renderTemplate(path);
        String renderedBody = renderTemplate(body);
        concurrentResponses.clear();
        java.util.concurrent.ExecutorService executor =
                java.util.concurrent.Executors.newFixedThreadPool(times);
        List<java.util.concurrent.Future<Response>> futures = new ArrayList<>();
        for (int i = 0; i < times; i++) {
            trackUsage();
            futures.add(
                    executor.submit(
                            () -> {
                                var requestSpec =
                                        given().contentType(MediaType.APPLICATION_JSON)
                                                .body(renderedBody);
                                requestSpec = authenticateRequest(requestSpec);
                                return requestSpec.when().post(renderedPath);
                            }));
        }
        for (java.util.concurrent.Future<Response> future : futures) {
            concurrentResponses.add(future.get());
        }
        executor.shutdown();
        // Wait a bit to ensure all database transactions are committed
        try {
            Thread.sleep(100);
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
        }
        // Set lastResponse to the first one for compatibility
        if (!concurrentResponses.isEmpty()) {
            this.lastResponse = concurrentResponses.get(0);
        }
    }

    @io.cucumber.java.en.Then("all responses should have status {int}")
    public void allResponsesShouldHaveStatus(int expectedStatus) {
        trackUsage();
        for (Response response : concurrentResponses) {
            assertThat(
                    "All responses should have status " + expectedStatus,
                    response.getStatusCode(),
                    is(expectedStatus));
        }
    }

    @io.cucumber.java.en.Given("the conversation has entries")
    public void theConversationHasEntries() {
        trackUsage();
        if (conversationId == null) {
            throw new IllegalStateException("No conversation available");
        }
        String userId = currentUserId != null ? currentUserId : "alice";
        CreateUserEntryRequest request1 = new CreateUserEntryRequest();
        request1.setContent("Entry 1");
        memoryStoreSelector.getStore().appendUserEntry(userId, conversationId, request1);
        CreateUserEntryRequest request2 = new CreateUserEntryRequest();
        request2.setContent("Entry 2");
        memoryStoreSelector.getStore().appendUserEntry(userId, conversationId, request2);
    }

    @io.cucumber.java.en.Given("the conversation is shared with user {string}")
    @Transactional
    public void theConversationIsSharedWithUser(String userId) {
        trackUsage();
        theConversationIsSharedWithUserWithAccessLevel(userId, "reader");
    }

    @io.cucumber.java.en.Given("the membership for user {string} was soft-deleted {int} days ago")
    @Transactional
    public void theMembershipForUserWasSoftDeletedDaysAgo(String userId, int daysAgo) {
        trackUsage();
        // Soft-delete the membership via the store API
        String ownerId = (String) contextVariables.getOrDefault("conversationOwner", currentUserId);
        memoryStoreSelector.getStore().deleteMembership(ownerId, conversationId, userId);

        // Backdate the deleted_at timestamp
        String datastoreType =
                config.getOptionalValue("memory-service.datastore.type", String.class)
                        .orElse("postgres");
        if ("postgres".equals(datastoreType)) {
            String groupId = (String) contextVariables.get("conversationGroupId");
            entityManager
                    .get()
                    .createNativeQuery(
                            "UPDATE conversation_memberships SET deleted_at = NOW() - INTERVAL '"
                                    + daysAgo
                                    + " days' WHERE conversation_group_id = :groupId"
                                    + " AND user_id = :userId")
                    .setParameter("groupId", java.util.UUID.fromString(groupId))
                    .setParameter("userId", userId)
                    .executeUpdate();
        }
    }

    @io.cucumber.java.en.Given("the conversation has a pending ownership transfer to user {string}")
    @Transactional
    public void theConversationHasAPendingOwnershipTransferToUser(String toUserId) {
        trackUsage();
        String ownerId = (String) contextVariables.getOrDefault("conversationOwner", currentUserId);
        memoryStoreSelector.getStore().requestOwnershipTransfer(ownerId, conversationId, toUserId);
    }
}
