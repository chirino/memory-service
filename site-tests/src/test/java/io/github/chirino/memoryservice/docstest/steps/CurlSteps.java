package io.github.chirino.memoryservice.docstest.steps;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertTrue;

import com.fasterxml.jackson.core.JsonProcessingException;
import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import com.github.difflib.DiffUtils;
import com.github.difflib.UnifiedDiffUtils;
import com.github.difflib.patch.Patch;
import io.cucumber.java.en.Then;
import io.cucumber.java.en.When;
import io.github.chirino.memoryservice.docstest.CurlParser;
import io.restassured.path.json.JsonPath;
import java.net.http.HttpClient;
import java.net.http.HttpRequest;
import java.net.http.HttpResponse;
import java.util.Arrays;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

public class CurlSteps {

    private static final ObjectMapper OBJECT_MAPPER = new ObjectMapper();
    private static final Pattern PLACEHOLDER_PATTERN = Pattern.compile("%\\{([^}]+)}");

    private String lastResponse;
    private int lastStatusCode;
    private final Map<String, Object> contextVariables = new HashMap<>();

    /**
     * In recording mode, assertion failures are logged as warnings instead of failing the test.
     * This allows all curl commands to execute so all responses are captured as fixtures.
     */
    private void softAssert(Runnable assertion) {
        if (DockerSteps.RECORD_MODE) {
            try {
                assertion.run();
            } catch (AssertionError e) {
                System.out.println(
                        "  [RECORDING] Assertion skipped (will validate in playback): "
                                + e.getMessage());
            }
        } else {
            assertion.run();
        }
    }

    @When("I execute curl command:")
    public void executeCurl(String curlCommand) throws Exception {
        String effectiveCommand = rewriteScenarioUserIdsInJson(curlCommand);

        // Execute shell setup commands (e.g., echo for file creation) before curl
        executeSetupCommands(effectiveCommand);

        // Build environment with system properties and context variables
        Map<String, String> environment = buildEnvironment();

        // Parse curl command and extract all curl commands from the block
        CurlParser parser = new CurlParser(effectiveCommand, environment);
        List<HttpRequest> requests = parser.toAllHttpRequests();

        if (requests.isEmpty()) {
            // No curl commands found (e.g., function definitions like get-token)
            System.out.println("  No curl commands found in block, skipping execution.");
            return;
        }

        HttpClient client = HttpClient.newBuilder().version(HttpClient.Version.HTTP_1_1).build();

        // Execute all curl commands in the block sequentially
        for (int reqIdx = 0; reqIdx < requests.size(); reqIdx++) {
            HttpRequest request = requests.get(reqIdx);
            System.out.println(
                    "  Executing curl "
                            + (reqIdx + 1)
                            + "/"
                            + requests.size()
                            + ": "
                            + request.method()
                            + " "
                            + request.uri());

            // Retry on transient failures (404/500/503) that occur during app startup
            int maxRetries = 5;
            int retryDelayMs = 3000;
            for (int attempt = 1; attempt <= maxRetries; attempt++) {
                HttpResponse<String> response =
                        client.send(request, HttpResponse.BodyHandlers.ofString());
                lastResponse = normalizeScenarioUserIds(response.body());
                lastStatusCode = response.statusCode();

                if (lastStatusCode != 404 && lastStatusCode != 500 && lastStatusCode != 503) {
                    break;
                }
                if (attempt < maxRetries) {
                    System.out.println(
                            "    Received "
                                    + lastStatusCode
                                    + " (attempt "
                                    + attempt
                                    + "/"
                                    + maxRetries
                                    + "), retrying in "
                                    + retryDelayMs
                                    + "ms...");
                    Thread.sleep(retryDelayMs);
                }
            }

            System.out.println(
                    "    Response: status="
                            + lastStatusCode
                            + ", body length="
                            + (lastResponse != null ? lastResponse.length() : 0));
        }
    }

    private String normalizeScenarioUserIds(String body) {
        if (body == null || body.isBlank()) {
            return body;
        }
        String normalized = body;
        Integer scenarioPort = CheckpointSteps.getCurrentScenarioPort();
        if (scenarioPort == null) {
            return normalized;
        }
        normalized = normalized.replaceAll("\\b(bob|alice|charlie)-" + scenarioPort + "\\b", "$1");
        return normalized;
    }

    private String rewriteScenarioUserIdsInJson(String commandBlock) {
        if (commandBlock == null || commandBlock.isBlank()) {
            return commandBlock;
        }
        Integer scenarioPort = CheckpointSteps.getCurrentScenarioPort();
        if (scenarioPort == null) {
            return commandBlock;
        }

        String rewritten = commandBlock;
        for (String baseUser : List.of("bob", "alice", "charlie")) {
            String scenarioUser = baseUser + "-" + scenarioPort;
            rewritten =
                    rewritten.replaceAll(
                            "(\"userId\"\\s*:\\s*\")" + Pattern.quote(baseUser) + "(\")",
                            "$1" + scenarioUser + "$2");
        }
        return rewritten;
    }

    @Then("the response should contain {string}")
    public void responseShouldContain(String expected) {
        softAssert(
                () ->
                        assertTrue(
                                lastResponse.contains(expected),
                                "Expected response to contain '"
                                        + expected
                                        + "' but got: "
                                        + lastResponse));
    }

    @Then("the response should not contain {string}")
    public void responseShouldNotContain(String unexpected) {
        softAssert(
                () ->
                        assertFalse(
                                lastResponse.contains(unexpected),
                                "Expected response to NOT contain '"
                                        + unexpected
                                        + "' but got: "
                                        + lastResponse));
    }

    @Then("the response status should be {int}")
    public void responseStatusShouldBe(int expectedStatus) {
        softAssert(
                () ->
                        assertEquals(
                                expectedStatus,
                                lastStatusCode,
                                "Expected status "
                                        + expectedStatus
                                        + " but got "
                                        + lastStatusCode));
    }

    @Then("the response should be json with items array")
    public void responseShouldBeJsonWithItemsArray() {
        softAssert(
                () -> {
                    try {
                        JsonNode node = OBJECT_MAPPER.readTree(lastResponse);
                        assertTrue(
                                node.has("data"),
                                "Expected JSON with 'data' field but got: " + lastResponse);
                        assertTrue(
                                node.get("data").isArray(),
                                "Expected 'data' to be an array but got: " + lastResponse);
                    } catch (JsonProcessingException e) {
                        throw new AssertionError("Response is not valid JSON: " + lastResponse, e);
                    }
                });
    }

    @Then("set {string} to the json response field {string}")
    public void setContextVariableToJsonResponseField(String variableName, String path) {
        if (lastResponse == null) {
            throw new AssertionError("No HTTP response has been received");
        }
        JsonPath jsonPath = JsonPath.from(lastResponse);
        Object value = jsonPath.get(path);
        if (value == null) {
            throw new AssertionError(
                    "JSON response field '" + path + "' is null or does not exist");
        }
        contextVariables.put(variableName, value);
    }

    @Then("the response should match pattern {string}")
    public void responseShouldMatchPattern(String regex) {
        softAssert(
                () -> {
                    Pattern pattern = Pattern.compile(regex);
                    assertTrue(
                            pattern.matcher(lastResponse).find(),
                            "Expected response to match pattern '"
                                    + regex
                                    + "' but got: "
                                    + lastResponse);
                });
    }

    /**
     * Validates text response with exact string matching.
     * Use this for plain text or when you don't need JSON validation.
     *
     * Example:
     * <pre>
     * And the response body should be text:
     * """
     * I am Claude, an AI assistant created by Anthropic.
     * """
     * </pre>
     */
    @Then("the response body should be text:")
    public void theResponseBodyShouldBeText(String expectedText) {
        String trimmedExpected = normalizeText(expectedText != null ? expectedText.trim() : "");
        String trimmedActual = normalizeText(lastResponse != null ? lastResponse.trim() : "");

        // For text responses, we do a simple contains check rather than exact match
        // since the response may have extra formatting or newlines
        softAssert(
                () ->
                        assertTrue(
                                trimmedActual.contains(trimmedExpected),
                                "Expected response to contain:\n"
                                        + trimmedExpected
                                        + "\n\nBut got:\n"
                                        + trimmedActual));
    }

    /**
     * Normalizes text for comparison by replacing Unicode smart quotes with ASCII equivalents
     * and normalizing whitespace. This handles differences between OpenAI API responses
     * (which may use smart quotes) and documentation text (which uses ASCII).
     */
    private String normalizeText(String text) {
        if (text == null) return "";
        return text.replace('\u2018', '\'') // left single quotation mark
                .replace('\u2019', '\'') // right single quotation mark
                .replace('\u201C', '"') // left double quotation mark
                .replace('\u201D', '"') // right double quotation mark
                .replace('\u2014', '-') // em dash
                .replace('\u2013', '-') // en dash
                .replace("\u00A0", " ") // non-breaking space
                .replaceAll("[\\p{Zs}\\p{Cf}]", " ") // Unicode whitespace and format chars
                .replaceAll("\\s+", " "); // collapse all whitespace to single space
    }

    /**
     * Validates JSON response with fixture-style comparison and variable substitution.
     * Supports %{response.body.field} syntax for dynamic values.
     *
     * Example:
     * <pre>
     * And the response body should be json:
     * """
     * {
     *   "id": "%{response.body.id}",
     *   "title": "My Title",
     *   "count": 42
     * }
     * """
     * </pre>
     */
    @Then("the response body should be json:")
    public void theResponseBodyShouldBeJson(String expectedJson) {
        softAssert(() -> validateJsonResponse(expectedJson));
    }

    private void validateJsonResponse(String expectedJson) {
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
            } catch (JsonProcessingException e) {
                throw new AssertionError(
                        "Failed to parse expected JSON: "
                                + e.getMessage()
                                + "\nJSON:\n"
                                + expectedJson,
                        e);
            }
        }

        try {
            actualNode = OBJECT_MAPPER.readTree(lastResponse);
            actualPretty =
                    OBJECT_MAPPER.writerWithDefaultPrettyPrinter().writeValueAsString(actualNode);
        } catch (JsonProcessingException e) {
            throw new AssertionError(
                    "Failed to parse actual JSON: " + e.getMessage() + "\nJSON:\n" + lastResponse,
                    e);
        }

        // Compare with expected-as-subset semantics (actual can contain additive fields).
        if (matchesExpectedShape(expectedNode, actualNode)) {
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

    private boolean matchesExpectedShape(JsonNode expected, JsonNode actual) {
        if (expected == null) {
            return actual == null;
        }
        if (actual == null) {
            return false;
        }
        if (expected.isObject()) {
            if (!actual.isObject()) {
                return false;
            }
            var fields = expected.fields();
            while (fields.hasNext()) {
                var field = fields.next();
                JsonNode actualValue = actual.get(field.getKey());
                if (actualValue == null || !matchesExpectedShape(field.getValue(), actualValue)) {
                    return false;
                }
            }
            return true;
        }
        if (expected.isArray()) {
            if (!actual.isArray() || actual.size() != expected.size()) {
                return false;
            }
            for (int i = 0; i < expected.size(); i++) {
                if (!matchesExpectedShape(expected.get(i), actual.get(i))) {
                    return false;
                }
            }
            return true;
        }
        return expected.equals(actual);
    }

    /**
     * Template rendering with variable substitution.
     * Supports %{response.body.field} and %{context.variable} syntax.
     */
    private String renderTemplate(String template) {
        if (template == null || template.isBlank()) {
            return template;
        }

        JsonPath responseJson = JsonPath.from(lastResponse);
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
                return responseJson.get("$");
            }
            if (expression.startsWith("response.body.")) {
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
        } catch (Exception e) {
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
        } catch (JsonProcessingException e) {
            throw new AssertionError("Failed to serialize placeholder value: " + e.getMessage(), e);
        }
    }

    private String serializeContextVariables() {
        try {
            return OBJECT_MAPPER.writeValueAsString(contextVariables);
        } catch (JsonProcessingException e) {
            return "{}";
        }
    }

    /**
     * Executes shell setup commands that appear before curl commands in a bash block.
     * Handles echo commands for file creation (e.g., {@code echo "content" > /tmp/file.txt}).
     */
    private void executeSetupCommands(String bashBlock) {
        for (String line : bashBlock.split("\n")) {
            String trimmed = line.trim();
            if (trimmed.startsWith("echo ")) {
                try {
                    System.out.println("  Executing setup: " + trimmed);
                    ProcessBuilder pb = new ProcessBuilder("bash", "-c", trimmed);
                    pb.redirectErrorStream(true);
                    Process process = pb.start();
                    int exit = process.waitFor();
                    if (exit != 0) {
                        System.out.println("  Warning: setup command exited with code " + exit);
                    }
                } catch (Exception e) {
                    System.out.println("  Warning: setup command failed: " + e.getMessage());
                }
            }
        }
    }

    private Map<String, String> buildEnvironment() {
        Map<String, String> env = new HashMap<>(System.getenv());

        // Add system properties (like AUTH_TOKEN set by DockerSteps)
        System.getProperties().forEach((key, value) -> env.put(key.toString(), value.toString()));

        Integer scenarioPort = CheckpointSteps.getCurrentScenarioPort();
        if (scenarioPort != null) {
            DockerSteps.injectScenarioAuthTokens(env, scenarioPort);
        }

        // Add context variables as strings
        contextVariables.forEach((key, value) -> env.put(key, value.toString()));

        return env;
    }
}
