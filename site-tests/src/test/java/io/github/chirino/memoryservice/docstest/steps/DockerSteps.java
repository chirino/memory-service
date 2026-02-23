package io.github.chirino.memoryservice.docstest.steps;

import static com.github.tomakehurst.wiremock.core.WireMockConfiguration.wireMockConfig;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.fasterxml.jackson.databind.node.ObjectNode;
import com.github.tomakehurst.wiremock.WireMockServer;
import io.cucumber.java.After;
import io.cucumber.java.Before;
import io.cucumber.java.en.Given;
import java.io.File;
import java.io.IOException;
import java.net.Socket;
import java.net.URI;
import java.net.URLEncoder;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Duration;
import java.time.Instant;
import java.util.ArrayList;
import java.util.Collections;
import java.util.Comparator;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.TimeUnit;
import java.util.stream.Collectors;

public class DockerSteps {

    private static boolean dockerAlreadyRunning = false;
    private static boolean shutdownHookRegistered = false;
    private static File projectRoot;
    private static WireMockServer wiremockServer;
    private static int wiremockPort = 8090;
    private static boolean wiremockStarted = false;
    private static boolean databaseCleared = false;

    private static final String RECORD_SETTING = System.getenv("SITE_TEST_RECORD");

    /** True when any recording mode is active (either "true" for incremental or "all" for full). */
    static final boolean RECORD_MODE =
            RECORD_SETTING != null
                    && !RECORD_SETTING.isEmpty()
                    && !"false".equalsIgnoreCase(RECORD_SETTING);

    /** True when recording should overwrite existing fixtures ("all" or "force"). */
    static final boolean RECORD_ALL =
            "all".equalsIgnoreCase(RECORD_SETTING) || "force".equalsIgnoreCase(RECORD_SETTING);

    private static final ObjectMapper OBJECT_MAPPER = new ObjectMapper();
    private static final HttpClient HTTP_CLIENT =
            HttpClient.newBuilder().connectTimeout(Duration.ofSeconds(10)).build();
    private static final String KEYCLOAK_URL = "http://localhost:8081";
    private static final String KEYCLOAK_REALM = "memory-service";
    private static final String KEYCLOAK_CLIENT_ID = "memory-service-client";
    private static final String KEYCLOAK_CLIENT_SECRET = "change-me";
    private static final String MASTER_ADMIN_USER = "admin";
    private static final String MASTER_ADMIN_PASSWORD = "admin";
    private static final Object AUTH_LOCK = new Object();
    private static final Map<Integer, Map<String, String>> SCENARIO_TOKENS =
            new ConcurrentHashMap<>();

    static {
        projectRoot = findProjectRoot();
    }

    private static File findProjectRoot() {
        File current = new File(System.getProperty("user.dir"));

        while (current != null) {
            File pomFile = new File(current, "pom.xml");
            if (pomFile.exists()) {
                File siteTests = new File(current, "site-tests");
                if (siteTests.exists() && siteTests.isDirectory()) {
                    return current;
                }
            }
            current = current.getParentFile();
        }

        File siteTestsDir = new File(System.getProperty("user.dir"));
        if (siteTestsDir.getName().equals("site-tests")) {
            return siteTestsDir.getParentFile();
        }

        throw new RuntimeException("Could not find project root directory");
    }

    private static void startWireMock() {
        try {
            System.out.println(
                    "Starting embedded WireMock"
                            + (RECORD_MODE ? " in RECORDING mode" : " in PLAYBACK mode")
                            + "...");

            // Stop any existing WireMock server (e.g., from a previous run that didn't clean up)
            if (wiremockServer != null) {
                try {
                    wiremockServer.stop();
                } catch (Exception e) {
                    System.out.println(
                            "Warning: failed to stop existing WireMock: " + e.getMessage());
                }
                wiremockServer = null;
            }

            // Kill any process holding the WireMock port (from a crashed previous run)
            killProcessOnPort(wiremockPort);

            // Start WireMock without file-based mappings to avoid DELETE /__admin/mappings
            // removing files from disk. All mappings are loaded via admin API per-checkpoint.
            wiremockServer =
                    new WireMockServer(wireMockConfig().port(wiremockPort).globalTemplating(true));

            wiremockServer.start();
            System.out.println("WireMock started on port: " + wiremockPort);

            // Wait for admin API to be ready
            waitForWireMockReady();

            if (RECORD_MODE) {
                System.out.println("Recording mode: clearing default chat-completions mapping...");
                clearChatCompletionsMappings();
            }
        } catch (Exception e) {
            throw new RuntimeException("Failed to start WireMock server", e);
        }
    }

    public static int getWireMockPort() {
        return wiremockPort;
    }

    public static String getWireMockBaseUrl() {
        return "http://localhost:" + wiremockPort;
    }

    public static File getProjectRoot() {
        return projectRoot;
    }

    private static void killProcessOnPort(int port) {
        try (Socket socket = new Socket("localhost", port)) {
            // Port is in use, try to kill the process
            System.out.println(
                    "Port " + port + " is in use, attempting to kill occupying process...");
            ProcessBuilder pb = new ProcessBuilder("lsof", "-t", "-i", ":" + port);
            pb.redirectErrorStream(true);
            Process process = pb.start();
            String output = new String(process.getInputStream().readAllBytes()).trim();
            process.waitFor(5, TimeUnit.SECONDS);
            if (!output.isEmpty()) {
                for (String pid : output.split("\\n")) {
                    pid = pid.trim();
                    if (!pid.isEmpty()) {
                        System.out.println("Killing process " + pid + " on port " + port);
                        new ProcessBuilder("kill", "-9", pid).start().waitFor(2, TimeUnit.SECONDS);
                    }
                }
                Thread.sleep(1000); // Wait for port to be released
            }
        } catch (IOException e) {
            // Port is free, nothing to do
        } catch (InterruptedException e) {
            Thread.currentThread().interrupt();
        }
    }

    private static void waitForWireMockReady() {
        Instant deadline = Instant.now().plus(Duration.ofSeconds(30));
        while (Instant.now().isBefore(deadline)) {
            try {
                httpGet(adminUrl("/mappings"));
                System.out.println("WireMock admin API is ready.");
                return;
            } catch (Exception e) {
                try {
                    Thread.sleep(500);
                } catch (InterruptedException ie) {
                    Thread.currentThread().interrupt();
                    throw new RuntimeException("Interrupted waiting for WireMock", ie);
                }
            }
        }
        throw new RuntimeException("WireMock admin API did not become ready within 30 seconds");
    }

    // ---- WireMock Admin API helpers ----

    private static String adminUrl(String path) {
        return "http://localhost:" + wiremockPort + "/__admin" + path;
    }

    private static String httpGet(String url) throws IOException, InterruptedException {
        HttpRequest request = HttpRequest.newBuilder().uri(URI.create(url)).GET().build();
        HttpResponse<String> response =
                HTTP_CLIENT.send(request, HttpResponse.BodyHandlers.ofString());
        return response.body();
    }

    private static String httpPost(String url, String body)
            throws IOException, InterruptedException {
        HttpRequest request =
                HttpRequest.newBuilder()
                        .uri(URI.create(url))
                        .header("Content-Type", "application/json")
                        .POST(HttpRequest.BodyPublishers.ofString(body))
                        .build();
        HttpResponse<String> response =
                HTTP_CLIENT.send(request, HttpResponse.BodyHandlers.ofString());
        return response.body();
    }

    private static String httpPostForm(String url, Map<String, String> formData)
            throws IOException, InterruptedException {
        String body =
                formData.entrySet().stream()
                        .map(
                                e ->
                                        URLEncoder.encode(e.getKey(), StandardCharsets.UTF_8)
                                                + "="
                                                + URLEncoder.encode(
                                                        e.getValue(), StandardCharsets.UTF_8))
                        .collect(Collectors.joining("&"));
        HttpRequest request =
                HttpRequest.newBuilder()
                        .uri(URI.create(url))
                        .header("Content-Type", "application/x-www-form-urlencoded")
                        .POST(HttpRequest.BodyPublishers.ofString(body))
                        .build();
        HttpResponse<String> response =
                HTTP_CLIENT.send(request, HttpResponse.BodyHandlers.ofString());
        if (response.statusCode() < 200 || response.statusCode() >= 300) {
            throw new RuntimeException(
                    "POST form failed ("
                            + response.statusCode()
                            + ") for "
                            + url
                            + ": "
                            + response.body());
        }
        return response.body();
    }

    private static int httpDelete(String url) throws IOException, InterruptedException {
        HttpRequest request = HttpRequest.newBuilder().uri(URI.create(url)).DELETE().build();
        HttpResponse<String> response =
                HTTP_CLIENT.send(request, HttpResponse.BodyHandlers.ofString());
        return response.statusCode();
    }

    private static void clearChatCompletionsMappings() {
        try {
            String mappingsJson = httpGet(adminUrl("/mappings"));
            JsonNode root = OBJECT_MAPPER.readTree(mappingsJson);
            JsonNode mappings = root.get("mappings");

            if (mappings != null && mappings.isArray()) {
                for (JsonNode mapping : mappings) {
                    JsonNode request = mapping.get("request");
                    if (request != null) {
                        JsonNode urlPath = request.get("urlPath");
                        if (urlPath != null && "/v1/chat/completions".equals(urlPath.asText())) {
                            String id = mapping.get("id").asText();
                            httpDelete(adminUrl("/mappings/" + id));
                            System.out.println("Removed default chat-completions mapping: " + id);
                        }
                    }
                }
            }
        } catch (Exception e) {
            System.err.println(
                    "Warning: Failed to clear chat-completions mappings: " + e.getMessage());
        }
    }

    static void resetWireMockForCheckpoint() {
        try {
            System.out.println("Resetting WireMock state for new checkpoint...");

            httpDelete(adminUrl("/mappings"));
            httpDelete(adminUrl("/requests"));

            File modelsFile = new File(projectRoot, "site-tests/openai-mock/mappings/models.json");
            String modelsMapping = Files.readString(modelsFile.toPath());
            httpPost(adminUrl("/mappings"), modelsMapping);

            if (RECORD_MODE) {
                String proxyMapping =
                        "{\"request\": {\"urlPattern\": \".*\"},"
                                + " \"response\": {\"proxyBaseUrl\":"
                                + " \"https://api.openai.com\"},"
                                + " \"priority\": 10}";
                httpPost(adminUrl("/mappings"), proxyMapping);
                System.out.println("Re-created proxy-all mapping for recording mode.");
            }

            httpPost(adminUrl("/scenarios/reset"), "");

            System.out.println("WireMock state reset complete.");
        } catch (Exception e) {
            throw new RuntimeException("Failed to reset WireMock for checkpoint", e);
        }
    }

    static void loadFixturesForCheckpoint(String checkpointId) {
        try {
            Path fixtureDir = getFixtureDir(checkpointId);
            System.out.println("Loading fixtures from: " + fixtureDir);

            if (!Files.exists(fixtureDir)) {
                System.out.println(
                        "No fixtures found for "
                                + checkpointId
                                + ", loading default chat-completions mapping.");
                loadDefaultChatCompletions();
                return;
            }

            List<Path> fixtureFiles =
                    Files.list(fixtureDir)
                            .filter(p -> p.toString().endsWith(".json"))
                            .sorted(Comparator.comparing(p -> p.getFileName().toString()))
                            .collect(Collectors.toList());

            if (fixtureFiles.isEmpty()) {
                System.out.println(
                        "No fixture files in "
                                + fixtureDir
                                + ", loading default chat-completions mapping.");
                loadDefaultChatCompletions();
                return;
            }

            for (Path fixture : fixtureFiles) {
                String stubJson = Files.readString(fixture);
                JsonNode stubNode = OBJECT_MAPPER.readTree(stubJson);
                if (!stubNode.has("priority")) {
                    ((ObjectNode) stubNode).put("priority", 1);
                    stubJson = OBJECT_MAPPER.writeValueAsString(stubNode);
                }
                String response = httpPost(adminUrl("/mappings"), stubJson);
                System.out.println("  Loaded fixture: " + fixture.getFileName());
                JsonNode responseNode = OBJECT_MAPPER.readTree(response);
                if (responseNode.has("errors")) {
                    System.err.println(
                            "  WARNING: WireMock returned errors for "
                                    + fixture.getFileName()
                                    + ": "
                                    + response);
                }
            }

            System.out.println("Loaded " + fixtureFiles.size() + " fixture(s) for " + checkpointId);

            String allMappingsJson = httpGet(adminUrl("/mappings"));
            JsonNode allMappingsNode = OBJECT_MAPPER.readTree(allMappingsJson);
            JsonNode mappingsArray = allMappingsNode.get("mappings");
            System.out.println(
                    "  WireMock total mappings after load: "
                            + (mappingsArray != null ? mappingsArray.size() : 0));
        } catch (Exception e) {
            throw new RuntimeException(
                    "Failed to load fixtures for checkpoint: " + checkpointId, e);
        }
    }

    private static void loadDefaultChatCompletions() throws IOException, InterruptedException {
        File chatCompletionsFile =
                new File(projectRoot, "site-tests/openai-mock/mappings/chat-completions.json");
        String mapping = Files.readString(chatCompletionsFile.toPath());
        httpPost(adminUrl("/mappings"), mapping);

        File streamingFile =
                new File(
                        projectRoot,
                        "site-tests/openai-mock/mappings/chat-completions-streaming.json");
        if (streamingFile.exists()) {
            String streamingMapping = Files.readString(streamingFile.toPath());
            httpPost(adminUrl("/mappings"), streamingMapping);
        }
    }

    static void saveFixturesFromJournal(String checkpointId) {
        try {
            System.out.println("Saving fixtures from WireMock journal for " + checkpointId + "...");

            String journalJson = httpGet(adminUrl("/requests"));
            JsonNode root = OBJECT_MAPPER.readTree(journalJson);
            JsonNode requests = root.get("requests");

            if (requests == null || !requests.isArray() || requests.isEmpty()) {
                System.out.println("No requests recorded in journal for " + checkpointId);
                return;
            }

            List<JsonNode> chatRequests = new ArrayList<>();
            for (JsonNode req : requests) {
                JsonNode requestNode = req.get("request");
                if (requestNode != null) {
                    String url =
                            requestNode.has("url")
                                    ? requestNode.get("url").asText()
                                    : requestNode.has("absoluteUrl")
                                            ? requestNode.get("absoluteUrl").asText()
                                            : "";
                    if (url.contains("/v1/chat/completions")) {
                        chatRequests.add(req);
                    }
                }
            }

            java.util.Collections.reverse(chatRequests);

            if (chatRequests.isEmpty()) {
                System.out.println("No chat completion requests recorded for " + checkpointId);
                return;
            }

            Path fixtureDir = getFixtureDir(checkpointId);
            Files.createDirectories(fixtureDir);

            if (Files.exists(fixtureDir)) {
                try (var files = Files.list(fixtureDir)) {
                    files.filter(p -> p.toString().endsWith(".json"))
                            .forEach(
                                    p -> {
                                        try {
                                            Files.delete(p);
                                        } catch (IOException e) {
                                            System.err.println("Warning: could not delete " + p);
                                        }
                                    });
                }
            }

            String scenarioName = "chat-sequence";
            for (int i = 0; i < chatRequests.size(); i++) {
                JsonNode journalEntry = chatRequests.get(i);
                JsonNode response = journalEntry.get("response");

                if (response == null) {
                    System.out.println("  Skipping request " + (i + 1) + " - no response recorded");
                    continue;
                }

                ObjectNode stub = OBJECT_MAPPER.createObjectNode();

                stub.put("scenarioName", scenarioName);
                stub.put("requiredScenarioState", i == 0 ? "Started" : "step-" + (i + 1));
                if (i < chatRequests.size() - 1) {
                    stub.put("newScenarioState", "step-" + (i + 2));
                }

                ObjectNode requestMatcher = OBJECT_MAPPER.createObjectNode();
                requestMatcher.put("method", "POST");
                requestMatcher.put("urlPath", "/v1/chat/completions");
                stub.set("request", requestMatcher);

                ObjectNode responseNode = OBJECT_MAPPER.createObjectNode();
                responseNode.put("status", response.get("status").asInt());

                ObjectNode headers = OBJECT_MAPPER.createObjectNode();
                headers.put("Content-Type", "application/json");
                responseNode.set("headers", headers);

                String responseBody = response.get("body").asText();
                responseNode.put("body", responseBody);

                stub.set("response", responseNode);

                String filename = String.format("%03d.json", i + 1);
                Path fixturePath = fixtureDir.resolve(filename);
                String prettyJson =
                        OBJECT_MAPPER.writerWithDefaultPrettyPrinter().writeValueAsString(stub);
                Files.writeString(fixturePath, prettyJson);

                System.out.println("  Saved fixture: " + filename);
            }

            System.out.println("Saved " + chatRequests.size() + " fixture(s) for " + checkpointId);
        } catch (Exception e) {
            throw new RuntimeException(
                    "Failed to save fixtures from journal for " + checkpointId, e);
        }
    }

    static void resetScenarios() {
        try {
            httpPost(adminUrl("/scenarios/reset"), "");
        } catch (Exception e) {
            System.err.println("Warning: Failed to reset WireMock scenarios: " + e.getMessage());
        }
    }

    static boolean hasFixtures(String checkpointId) {
        Path fixtureDir = getFixtureDir(checkpointId);
        if (!Files.exists(fixtureDir)) {
            return false;
        }
        try (var files = Files.list(fixtureDir)) {
            return files.anyMatch(p -> p.toString().endsWith(".json"));
        } catch (IOException e) {
            return false;
        }
    }

    static Path getFixtureDir(String checkpointId) {
        String[] parts = checkpointId.split("/");
        String framework = parts[0];
        String checkpointName = parts[parts.length - 1];

        return projectRoot
                .toPath()
                .resolve("site-tests/openai-mock/fixtures")
                .resolve(framework)
                .resolve(checkpointName);
    }

    @Before
    public void setup() {
        if (!wiremockStarted) {
            startWireMock();
            wiremockStarted = true;
        }

        if (!dockerAlreadyRunning) {
            startDockerCompose();
            dockerAlreadyRunning = true;
        }

        clearDatabaseOnce();
    }

    private void clearDatabaseOnce() {
        if (databaseCleared) {
            return;
        }
        synchronized (DockerSteps.class) {
            if (databaseCleared) {
                return;
            }
            clearDatabase();
            databaseCleared = true;
        }
    }

    private void clearDatabase() {
        try {
            ProcessBuilder pb =
                    new ProcessBuilder(
                            "docker",
                            "exec",
                            "memory-service-postgres-1",
                            "psql",
                            "-U",
                            "postgres",
                            "-d",
                            "memory_service",
                            "-c",
                            "TRUNCATE entries, attachments, conversation_memberships,"
                                    + " conversation_ownership_transfers, conversations,"
                                    + " conversation_groups, tasks CASCADE");
            pb.directory(projectRoot);
            pb.redirectErrorStream(true);
            Process process = pb.start();
            String output = new String(process.getInputStream().readAllBytes());
            int exitCode = process.waitFor();
            if (exitCode != 0) {
                System.err.println("Warning: Failed to clear database: " + output);
            } else {
                System.out.println("Database cleared before scenario.");
            }
        } catch (Exception e) {
            System.err.println("Warning: Failed to clear database: " + e.getMessage());
        }
    }

    @Given("the memory-service is running via docker compose")
    public void memoryServiceIsRunning() {
        // Verify memory-service is responding (port 8082 is the host mapping in compose.yaml)
        waitForService("localhost", 8082, Duration.ofSeconds(60));
    }

    @Given("I set up authentication tokens")
    public void setupAuthTokens() {
        Integer scenarioPort = CheckpointSteps.getCurrentScenarioPort();
        if (scenarioPort != null) {
            Map<String, String> env = new HashMap<>();
            injectScenarioAuthTokens(env, scenarioPort);
            env.forEach(System::setProperty);
            return;
        }

        // Fallback for non-checkpoint scenarios.
        try {
            String bobToken = getAuthToken("bob", "bob");
            System.setProperty("CMD_get-token", bobToken);
            System.setProperty("CMD_get-token_bob_bob", bobToken);
        } catch (RuntimeException e) {
            System.out.println("Warning: Could not get fallback token for bob: " + e.getMessage());
        }
    }

    private void startDockerCompose() {
        try {
            ProcessBuilder pb = new ProcessBuilder("docker", "compose", "up", "-d");
            pb.directory(projectRoot);
            pb.environment().put("OPENAI_API_KEY", "not-needed-for-tests");
            pb.inheritIO();

            Process process = pb.start();
            int exitCode = process.waitFor();

            if (exitCode != 0) {
                throw new RuntimeException("Docker compose failed to start");
            }

            // Wait for services to be ready (port 8082 is the host mapping in compose.yaml)
            System.out.println("Waiting for memory-service to be ready...");
            waitForService("localhost", 8082, Duration.ofSeconds(120));

            System.out.println("Waiting for Keycloak to be ready...");
            waitForService("localhost", 8081, Duration.ofSeconds(120));

        } catch (Exception e) {
            throw new RuntimeException("Failed to start docker compose", e);
        }
    }

    private static void waitForService(String host, int port, Duration timeout) {
        Instant deadline = Instant.now().plus(timeout);

        while (Instant.now().isBefore(deadline)) {
            try (Socket socket = new Socket(host, port)) {
                return;
            } catch (IOException e) {
                try {
                    Thread.sleep(1000);
                } catch (InterruptedException ie) {
                    Thread.currentThread().interrupt();
                    throw new RuntimeException("Interrupted while waiting for service", ie);
                }
            }
        }

        throw new RuntimeException(
                String.format("Service %s:%d did not become ready within %s", host, port, timeout));
    }

    private String getAuthToken(String username, String password) {
        try {
            String responseBody =
                    httpPostForm(
                            KEYCLOAK_URL
                                    + "/realms/"
                                    + KEYCLOAK_REALM
                                    + "/protocol/openid-connect/token",
                            Map.of(
                                    "client_id", KEYCLOAK_CLIENT_ID,
                                    "client_secret", KEYCLOAK_CLIENT_SECRET,
                                    "grant_type", "password",
                                    "username", username,
                                    "password", password));
            JsonNode response = OBJECT_MAPPER.readTree(responseBody);
            JsonNode accessToken = response.get("access_token");
            if (accessToken == null || accessToken.asText().isBlank()) {
                throw new RuntimeException(
                        "No access_token in token response for " + username + ": " + responseBody);
            }
            return accessToken.asText();
        } catch (Exception e) {
            throw new RuntimeException("Failed to get auth token for " + username, e);
        }
    }

    static void injectScenarioAuthTokens(Map<String, String> env, int scenarioPort) {
        Map<String, String> tokens = ensureScenarioAuthTokens(scenarioPort);
        env.put("CMD_get-token", tokens.get("bob"));
        env.put("CMD_get-token_bob_bob", tokens.get("bob"));
        env.put("CMD_get-token_alice_alice", tokens.get("alice"));
        env.put("CMD_get-token_charlie_charlie", tokens.get("charlie"));
    }

    private static Map<String, String> ensureScenarioAuthTokens(int scenarioPort) {
        Map<String, String> cached = SCENARIO_TOKENS.get(scenarioPort);
        if (cached != null) {
            return cached;
        }
        synchronized (AUTH_LOCK) {
            cached = SCENARIO_TOKENS.get(scenarioPort);
            if (cached != null) {
                return cached;
            }

            Map<String, String> tokens = new HashMap<>();
            for (String user : List.of("bob", "alice", "charlie")) {
                String scenarioUsername = scenarioUser(user, scenarioPort);
                String scenarioPassword = scenarioUsername;
                List<String> roles =
                        "alice".equals(user) ? List.of("user", "admin") : List.of("user");
                ensureRealmUser(scenarioUsername, scenarioPassword, roles);
                String token = getAuthTokenStatic(scenarioUsername, scenarioPassword);
                tokens.put(user, token);
            }
            Map<String, String> immutable = Collections.unmodifiableMap(tokens);
            SCENARIO_TOKENS.put(scenarioPort, immutable);
            return immutable;
        }
    }

    private static String scenarioUser(String baseUser, int scenarioPort) {
        return baseUser + "-" + scenarioPort;
    }

    private static void ensureRealmUser(String username, String password, List<String> roles) {
        String adminToken = getMasterAdminToken();
        String userId = getUserId(adminToken, username);
        if (userId == null) {
            createUser(adminToken, username, password);
            userId = getUserId(adminToken, username);
        } else {
            resetPassword(adminToken, userId, password);
        }
        if (userId == null) {
            throw new RuntimeException("Unable to create/find Keycloak user " + username);
        }
        reconcileUserState(adminToken, userId, username);
        ensureRealmRoles(adminToken, userId, roles);
    }

    private static String getMasterAdminToken() {
        try {
            String responseBody =
                    httpPostForm(
                            KEYCLOAK_URL + "/realms/master/protocol/openid-connect/token",
                            Map.of(
                                    "client_id",
                                    "admin-cli",
                                    "grant_type",
                                    "password",
                                    "username",
                                    MASTER_ADMIN_USER,
                                    "password",
                                    MASTER_ADMIN_PASSWORD));
            JsonNode response = OBJECT_MAPPER.readTree(responseBody);
            return response.path("access_token").asText();
        } catch (Exception e) {
            throw new RuntimeException("Failed to get Keycloak master admin token", e);
        }
    }

    private static String getUserId(String adminToken, String username) {
        try {
            String url =
                    KEYCLOAK_URL
                            + "/admin/realms/"
                            + KEYCLOAK_REALM
                            + "/users?username="
                            + URLEncoder.encode(username, StandardCharsets.UTF_8)
                            + "&exact=true";
            HttpRequest request =
                    HttpRequest.newBuilder()
                            .uri(URI.create(url))
                            .header("Authorization", "Bearer " + adminToken)
                            .header("Content-Type", "application/json")
                            .GET()
                            .build();
            HttpResponse<String> response =
                    HTTP_CLIENT.send(request, HttpResponse.BodyHandlers.ofString());
            if (response.statusCode() != 200) {
                throw new RuntimeException(
                        "Keycloak user lookup failed ("
                                + response.statusCode()
                                + "): "
                                + response.body());
            }
            JsonNode users = OBJECT_MAPPER.readTree(response.body());
            if (!users.isArray() || users.isEmpty()) {
                return null;
            }
            return users.get(0).path("id").asText(null);
        } catch (Exception e) {
            throw new RuntimeException("Failed to query Keycloak user " + username, e);
        }
    }

    private static void createUser(String adminToken, String username, String password) {
        try {
            String namePrefix = username.replace('-', ' ');
            String body =
                    OBJECT_MAPPER.writeValueAsString(
                            Map.of(
                                    "username",
                                    username,
                                    "firstName",
                                    namePrefix,
                                    "lastName",
                                    "Scenario",
                                    "email",
                                    username + "@example.com",
                                    "enabled",
                                    true,
                                    "emailVerified",
                                    true,
                                    "requiredActions",
                                    List.of(),
                                    "credentials",
                                    List.of(
                                            Map.of(
                                                    "type",
                                                    "password",
                                                    "value",
                                                    password,
                                                    "temporary",
                                                    false))));
            HttpRequest request =
                    HttpRequest.newBuilder()
                            .uri(
                                    URI.create(
                                            KEYCLOAK_URL
                                                    + "/admin/realms/"
                                                    + KEYCLOAK_REALM
                                                    + "/users"))
                            .header("Authorization", "Bearer " + adminToken)
                            .header("Content-Type", "application/json")
                            .POST(HttpRequest.BodyPublishers.ofString(body))
                            .build();
            HttpResponse<String> response =
                    HTTP_CLIENT.send(request, HttpResponse.BodyHandlers.ofString());
            if (response.statusCode() != 201 && response.statusCode() != 409) {
                throw new RuntimeException(
                        "Keycloak user create failed for "
                                + username
                                + " ("
                                + response.statusCode()
                                + "): "
                                + response.body());
            }
        } catch (Exception e) {
            throw new RuntimeException("Failed to create Keycloak user " + username, e);
        }
    }

    private static void reconcileUserState(String adminToken, String userId, String username) {
        try {
            String namePrefix = username.replace('-', ' ');
            String body =
                    OBJECT_MAPPER.writeValueAsString(
                            Map.of(
                                    "username",
                                    username,
                                    "firstName",
                                    namePrefix,
                                    "lastName",
                                    "Scenario",
                                    "email",
                                    username + "@example.com",
                                    "enabled",
                                    true,
                                    "emailVerified",
                                    true,
                                    "requiredActions",
                                    List.of()));
            HttpRequest request =
                    HttpRequest.newBuilder()
                            .uri(
                                    URI.create(
                                            KEYCLOAK_URL
                                                    + "/admin/realms/"
                                                    + KEYCLOAK_REALM
                                                    + "/users/"
                                                    + userId))
                            .header("Authorization", "Bearer " + adminToken)
                            .header("Content-Type", "application/json")
                            .PUT(HttpRequest.BodyPublishers.ofString(body))
                            .build();
            HttpResponse<String> response =
                    HTTP_CLIENT.send(request, HttpResponse.BodyHandlers.ofString());
            if (response.statusCode() != 204) {
                throw new RuntimeException(
                        "Keycloak user reconcile failed ("
                                + response.statusCode()
                                + "): "
                                + response.body());
            }
        } catch (Exception e) {
            throw new RuntimeException("Failed to reconcile Keycloak user " + username, e);
        }
    }

    private static void resetPassword(String adminToken, String userId, String password) {
        try {
            String body =
                    OBJECT_MAPPER.writeValueAsString(
                            Map.of("type", "password", "value", password, "temporary", false));
            HttpRequest request =
                    HttpRequest.newBuilder()
                            .uri(
                                    URI.create(
                                            KEYCLOAK_URL
                                                    + "/admin/realms/"
                                                    + KEYCLOAK_REALM
                                                    + "/users/"
                                                    + userId
                                                    + "/reset-password"))
                            .header("Authorization", "Bearer " + adminToken)
                            .header("Content-Type", "application/json")
                            .PUT(HttpRequest.BodyPublishers.ofString(body))
                            .build();
            HttpResponse<String> response =
                    HTTP_CLIENT.send(request, HttpResponse.BodyHandlers.ofString());
            if (response.statusCode() != 204) {
                throw new RuntimeException(
                        "Keycloak password reset failed ("
                                + response.statusCode()
                                + "): "
                                + response.body());
            }
        } catch (Exception e) {
            throw new RuntimeException("Failed to reset password for userId " + userId, e);
        }
    }

    private static void ensureRealmRoles(String adminToken, String userId, List<String> roleNames) {
        try {
            List<Map<String, Object>> roles = new ArrayList<>();
            for (String roleName : roleNames) {
                HttpRequest roleRequest =
                        HttpRequest.newBuilder()
                                .uri(
                                        URI.create(
                                                KEYCLOAK_URL
                                                        + "/admin/realms/"
                                                        + KEYCLOAK_REALM
                                                        + "/roles/"
                                                        + roleName))
                                .header("Authorization", "Bearer " + adminToken)
                                .GET()
                                .build();
                HttpResponse<String> roleResponse =
                        HTTP_CLIENT.send(roleRequest, HttpResponse.BodyHandlers.ofString());
                if (roleResponse.statusCode() != 200) {
                    throw new RuntimeException(
                            "Failed to fetch role "
                                    + roleName
                                    + " ("
                                    + roleResponse.statusCode()
                                    + "): "
                                    + roleResponse.body());
                }
                JsonNode roleNode = OBJECT_MAPPER.readTree(roleResponse.body());
                roles.add(
                        Map.of(
                                "id", roleNode.path("id").asText(),
                                "name", roleNode.path("name").asText()));
            }

            String body = OBJECT_MAPPER.writeValueAsString(roles);
            HttpRequest mappingRequest =
                    HttpRequest.newBuilder()
                            .uri(
                                    URI.create(
                                            KEYCLOAK_URL
                                                    + "/admin/realms/"
                                                    + KEYCLOAK_REALM
                                                    + "/users/"
                                                    + userId
                                                    + "/role-mappings/realm"))
                            .header("Authorization", "Bearer " + adminToken)
                            .header("Content-Type", "application/json")
                            .POST(HttpRequest.BodyPublishers.ofString(body))
                            .build();
            HttpResponse<String> mappingResponse =
                    HTTP_CLIENT.send(mappingRequest, HttpResponse.BodyHandlers.ofString());
            if (mappingResponse.statusCode() != 204) {
                throw new RuntimeException(
                        "Failed to map roles for userId "
                                + userId
                                + " ("
                                + mappingResponse.statusCode()
                                + "): "
                                + mappingResponse.body());
            }
        } catch (Exception e) {
            throw new RuntimeException("Failed to assign realm roles for userId " + userId, e);
        }
    }

    private static String getAuthTokenStatic(String username, String password) {
        try {
            String responseBody =
                    httpPostForm(
                            KEYCLOAK_URL
                                    + "/realms/"
                                    + KEYCLOAK_REALM
                                    + "/protocol/openid-connect/token",
                            Map.of(
                                    "client_id", KEYCLOAK_CLIENT_ID,
                                    "client_secret", KEYCLOAK_CLIENT_SECRET,
                                    "grant_type", "password",
                                    "username", username,
                                    "password", password));
            JsonNode response = OBJECT_MAPPER.readTree(responseBody);
            JsonNode accessToken = response.get("access_token");
            if (accessToken == null || accessToken.asText().isBlank()) {
                throw new RuntimeException(
                        "No access_token in token response for " + username + ": " + responseBody);
            }
            return accessToken.asText();
        } catch (Exception e) {
            throw new RuntimeException("Failed to get auth token for " + username, e);
        }
    }

    @After
    public void cleanup() {
        // Keep docker compose running between scenarios for speed
        // Only stop at JVM shutdown (register hook only once)
        if (!shutdownHookRegistered) {
            shutdownHookRegistered = true;
            Runtime.getRuntime()
                    .addShutdownHook(
                            new Thread(
                                    () -> {
                                        if (wiremockServer != null && wiremockServer.isRunning()) {
                                            System.out.println("Stopping WireMock server...");
                                            wiremockServer.stop();
                                        }
                                        stopDockerCompose();
                                    }));
        }
    }

    private void stopDockerCompose() {
        try {
            ProcessBuilder pb = new ProcessBuilder("docker", "compose", "down");
            pb.directory(projectRoot);
            pb.environment().put("OPENAI_API_KEY", "not-needed-for-tests");
            pb.inheritIO();

            Process process = pb.start();
            process.waitFor(30, TimeUnit.SECONDS);

        } catch (Exception e) {
            System.err.println("Warning: Failed to stop docker compose: " + e.getMessage());
        }
    }
}
