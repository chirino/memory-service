package io.github.chirino.memory.cucumber;

import static io.restassured.RestAssured.given;
import static org.hamcrest.MatcherAssert.assertThat;
import static org.hamcrest.Matchers.hasSize;
import static org.hamcrest.Matchers.is;
import static org.hamcrest.Matchers.notNullValue;

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
import io.github.chirino.memory.grpc.v1.CreateSummaryRequest;
import io.github.chirino.memory.grpc.v1.HealthResponse;
import io.github.chirino.memory.grpc.v1.ListMessagesRequest;
import io.github.chirino.memory.grpc.v1.ListMessagesResponse;
import io.github.chirino.memory.grpc.v1.MessagesServiceGrpc;
import io.github.chirino.memory.grpc.v1.SearchServiceGrpc;
import io.github.chirino.memory.grpc.v1.SystemServiceGrpc;
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
import java.util.regex.Matcher;
import java.util.regex.Pattern;
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

    @io.cucumber.java.Before(order = 0)
    public void setupGrpcChannel() {
        if (grpcChannel != null) {
            return;
        }
        String target =
                ConfigProvider.getConfig()
                        .getOptionalValue("test.url", String.class)
                        .orElse("http://localhost:8081");
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
        grpcChannel = ManagedChannelBuilder.forAddress(host, port).usePlaintext().build();
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

    @io.cucumber.java.en.Given("there is a conversation owned by {string}")
    public void thereIsAConversationOwnedBy(String ownerId) {
        CreateConversationRequest request = new CreateConversationRequest();
        request.setTitle("Owned by " + ownerId);
        this.conversationId =
                memoryStoreSelector.getStore().createConversation(ownerId, request).getId();
        contextVariables.put("conversationId", conversationId);
    }

    @io.cucumber.java.en.When("I list messages for the conversation")
    public void iListMessagesForTheConversation() {
        iListMessagesForTheConversationWithParams(null, null, null);
    }

    @io.cucumber.java.en.When("I list messages with limit {int}")
    public void iListMessagesWithLimit(int limit) {
        iListMessagesForTheConversationWithParams(null, limit, null);
    }

    @io.cucumber.java.en.When("I list messages for the conversation with channel {string}")
    public void iListMessagesForTheConversationWithChannel(String channel) {
        iListMessagesForTheConversationWithParams(null, null, channel);
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
            String after, Integer limit, String channel) {
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

    @io.cucumber.java.en.Then("the conversation title should be {string}")
    public void theConversationTitleShouldBe(String expectedTitle) {
        var dto = memoryStoreSelector.getStore().getConversation(currentUserId, conversationId);
        assertThat(dto.getTitle(), is(expectedTitle));
    }

    @io.cucumber.java.en.Given("I set context variable {string} to {string}")
    public void iSetContextVariableTo(String name, String value) {
        contextVariables.put(name, value);
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
        JsonPath contextJson = JsonPath.from(serializeContextVariables());

        Matcher matcher = PLACEHOLDER_PATTERN.matcher(template);
        StringBuilder result = new StringBuilder();
        int lastIndex = 0;
        while (matcher.find()) {
            result.append(template, lastIndex, matcher.start());
            String expression = matcher.group(1).trim();
            Object value = resolveExpression(expression, responseJson, contextJson);
            boolean inQuotes = isSurroundedByQuotes(template, matcher.start(), matcher.end());
            result.append(serializeReplacement(value, inQuotes));
            lastIndex = matcher.end();
        }
        result.append(template.substring(lastIndex));
        return result.toString();
    }

    private Object resolveExpression(
            String expression, JsonPath responseJson, JsonPath contextJson) {
        try {
            if (expression.equals("response.body")) {
                ensureHttpResponseAvailable(expression, responseJson);
                return responseJson.get("$");
            }
            if (expression.startsWith("response.body.")) {
                ensureHttpResponseAvailable(expression, responseJson);
                String path = expression.substring("response.body.".length());
                return responseJson.get(path);
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
            case "SystemService/GetHealth" -> HealthResponse.newBuilder();
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
        return switch (method) {
            case "ListMessages" -> {
                var requestBuilder = ListMessagesRequest.newBuilder();
                if (body != null && !body.isBlank()) {
                    TextFormat.merge(body, requestBuilder);
                }
                yield stub.listMessages(requestBuilder.build());
            }
            case "AppendMessage" -> {
                var requestBuilder = AppendMessageRequest.newBuilder();
                if (body != null && !body.isBlank()) {
                    TextFormat.merge(body, requestBuilder);
                }
                yield stub.appendMessage(requestBuilder.build());
            }
            default ->
                    throw new IllegalArgumentException(
                            "Unsupported MessagesService method: " + method);
        };
    }

    private Message callSearchService(String method, Metadata metadata, String body)
            throws Exception {
        var stub = SearchServiceGrpc.newBlockingStub(grpcChannel);
        if (metadata != null) {
            stub = stub.withInterceptors(MetadataUtils.newAttachHeadersInterceptor(metadata));
        }
        return switch (method) {
            case "CreateSummary" -> {
                var requestBuilder = CreateSummaryRequest.newBuilder();
                if (body != null && !body.isBlank()) {
                    TextFormat.merge(body, requestBuilder);
                }
                yield stub.createSummary(requestBuilder.build());
            }
            default ->
                    throw new IllegalArgumentException(
                            "Unsupported SearchService method: " + method);
        };
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
