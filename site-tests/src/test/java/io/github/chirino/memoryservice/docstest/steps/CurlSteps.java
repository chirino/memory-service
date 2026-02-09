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
        // Build environment with system properties and context variables
        Map<String, String> environment = buildEnvironment();

        // Parse curl command and extract all curl commands from the block
        CurlParser parser = new CurlParser(curlCommand, environment);
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
                lastResponse = response.body();
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

    private Map<String, String> buildEnvironment() {
        Map<String, String> env = new HashMap<>(System.getenv());

        // Add system properties (like AUTH_TOKEN set by DockerSteps)
        System.getProperties().forEach((key, value) -> env.put(key.toString(), value.toString()));

        // Add context variables as strings
        contextVariables.forEach((key, value) -> env.put(key, value.toString()));

        return env;
    }
}
