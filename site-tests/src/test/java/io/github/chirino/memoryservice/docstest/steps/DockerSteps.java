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
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Duration;
import java.time.Instant;
import java.util.ArrayList;
import java.util.Comparator;
import java.util.List;
import java.util.concurrent.TimeUnit;
import java.util.stream.Collectors;

public class DockerSteps {

    private static boolean dockerAlreadyRunning = false;
    private static boolean shutdownHookRegistered = false;
    private static File projectRoot;
    private static WireMockServer wiremockServer;
    private static int wiremockPort = 8090;
    private static boolean wiremockStarted = false;

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

        clearDatabase();
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
        for (String user : new String[] {"bob", "alice", "charlie"}) {
            try {
                String token = getAuthToken(user, user);
                System.setProperty("CMD_get-token_" + user + "_" + user, token);
            } catch (RuntimeException e) {
                System.out.println(
                        "Warning: Could not get token for " + user + ": " + e.getMessage());
            }
        }
        String bobToken = System.getProperty("CMD_get-token_bob_bob");
        System.setProperty("CMD_get-token", bobToken);
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
            ProcessBuilder pb =
                    new ProcessBuilder(
                            "curl",
                            "-sSfX",
                            "POST",
                            "http://localhost:8081/realms/memory-service/protocol/openid-connect/token",
                            "-H",
                            "Content-Type: application/x-www-form-urlencoded",
                            "-d",
                            "client_id=memory-service-client",
                            "-d",
                            "client_secret=change-me",
                            "-d",
                            "grant_type=password",
                            "-d",
                            "username=" + username,
                            "-d",
                            "password=" + password);

            pb.redirectErrorStream(true);
            Process process = pb.start();

            String output = new String(process.getInputStream().readAllBytes());
            int exitCode = process.waitFor();

            if (exitCode != 0) {
                throw new RuntimeException(
                        "Failed to get auth token for " + username + ": " + output);
            }

            java.util.regex.Pattern pattern =
                    java.util.regex.Pattern.compile("\"access_token\"\\s*:\\s*\"([^\"]+)\"");
            java.util.regex.Matcher matcher = pattern.matcher(output);

            if (matcher.find()) {
                return matcher.group(1);
            }

            throw new RuntimeException("Could not parse access_token from: " + output);

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
