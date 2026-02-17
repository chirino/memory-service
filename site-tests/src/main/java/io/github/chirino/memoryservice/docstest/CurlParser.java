package io.github.chirino.memoryservice.docstest;

import java.io.ByteArrayOutputStream;
import java.net.URI;
import java.net.http.HttpRequest;
import java.net.http.HttpRequest.BodyPublishers;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Path;
import java.time.Duration;
import java.util.ArrayList;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

/**
 * Converts curl commands to Java HttpClient requests
 */
public class CurlParser {

    private final String curlCommand;
    private final Map<String, String> environment;

    public CurlParser(String curlCommand) {
        this(curlCommand, System.getenv());
    }

    public CurlParser(String curlCommand, Map<String, String> environment) {
        this.curlCommand = curlCommand;
        this.environment = new HashMap<>(environment);
    }

    public HttpRequest toHttpRequest() throws Exception {
        String normalized = normalizeCurlCommand(curlCommand);
        return buildHttpRequest(normalized);
    }

    /**
     * Returns all curl commands from the bash block as HttpRequest objects.
     * If the bash block contains multiple curl commands, all are returned in order.
     */
    public List<HttpRequest> toAllHttpRequests() throws Exception {
        List<String> curlCommands = extractAllCurlCommands(curlCommand);
        List<HttpRequest> requests = new ArrayList<>();
        for (String cmd : curlCommands) {
            String normalized = normalizeSingleCurl(cmd);
            requests.add(buildHttpRequest(normalized));
        }
        return requests;
    }

    private HttpRequest buildHttpRequest(String normalized) throws Exception {
        HttpRequest.Builder builder = HttpRequest.newBuilder();

        // Extract URL
        String url = extractUrl(normalized);
        url = replaceEnvironmentVariables(url);
        builder.uri(URI.create(url));

        // Extract method (default to GET if not specified)
        String method = extractMethod(normalized);

        // Extract headers
        Map<String, String> headers = extractHeaders(normalized);
        for (Map.Entry<String, String> header : headers.entrySet()) {
            String value = replaceEnvironmentVariables(header.getValue());
            builder.header(header.getKey(), value);
        }

        // Check for multipart form data (-F) first, then regular body (-d)
        List<String[]> formFields = extractFormFields(normalized);
        if (!formFields.isEmpty()) {
            String boundary = "----JavaBoundary" + System.currentTimeMillis();
            builder.header("Content-Type", "multipart/form-data; boundary=" + boundary);
            byte[] multipartBody = buildMultipartBody(formFields, boundary);
            builder.method(method, BodyPublishers.ofByteArray(multipartBody));
        } else {
            String body = extractBody(normalized);
            if (body != null) {
                body = replaceEnvironmentVariables(body);
                builder.method(method, BodyPublishers.ofString(body));
            } else {
                builder.method(method, BodyPublishers.noBody());
            }
        }

        // Set timeout
        builder.timeout(Duration.ofSeconds(30));

        return builder.build();
    }

    private String normalizeCurlCommand(String curl) {
        // Extract just the first curl command from a potentially multi-line bash block.
        // Skip comment lines and non-curl lines before the curl command.
        String extracted = extractFirstCurlCommand(curl);

        // Remove curl prefix
        String normalized = extracted.replaceFirst("^curl\\s+", "");

        // Join line continuations
        normalized = normalized.replaceAll("\\\\\\s*\\n\\s*", " ");

        // Strip pipe commands (e.g., "| jq", "| jq -r '.data[0].id'") and everything after
        int pipeJq = normalized.indexOf("| jq");
        if (pipeJq >= 0) {
            normalized = normalized.substring(0, pipeJq);
        }

        // Strip comment lines (lines starting with #)
        normalized = normalized.replaceAll("(?m)^\\s*#.*$", "");

        // Normalize whitespace
        normalized = normalized.replaceAll("\\s+", " ");

        return normalized.trim();
    }

    /**
     * Extracts ALL curl commands from a multi-line bash block.
     * Returns each curl command as a separate string.
     */
    private List<String> extractAllCurlCommands(String bash) {
        List<String> commands = new ArrayList<>();
        String[] lines = bash.split("\n");
        StringBuilder curlCmd = null;
        boolean inCurl = false;
        boolean quoteOpen = false;
        int functionBraceDepth = 0;

        for (String line : lines) {
            String trimmed = line.trim();

            // Track function definitions — skip curl commands inside them
            if (trimmed.matches("^(function\\s+\\w+|\\w+\\s*\\(\\s*\\))\\s*\\{?.*")) {
                functionBraceDepth++;
                if (trimmed.contains("{")) {
                    // Opening brace is on the same line
                } else {
                    // Brace may come on the next line
                }
                continue;
            }
            if (functionBraceDepth > 0) {
                if (trimmed.contains("{")) functionBraceDepth++;
                if (trimmed.contains("}")) functionBraceDepth--;
                continue;
            }

            if (!inCurl) {
                if (trimmed.startsWith("curl")) {
                    inCurl = true;
                    curlCmd = new StringBuilder();
                    quoteOpen = false;
                } else {
                    continue;
                }
            }

            curlCmd.append(line).append("\n");

            for (char c : trimmed.toCharArray()) {
                if (c == '\'') quoteOpen = !quoteOpen;
            }

            if (!quoteOpen && !trimmed.endsWith("\\")) {
                commands.add(curlCmd.toString().trim());
                inCurl = false;
                curlCmd = null;
            }
        }

        // If we were still building a curl command, add it
        if (curlCmd != null && curlCmd.length() > 0) {
            commands.add(curlCmd.toString().trim());
        }

        return commands;
    }

    /**
     * Normalizes a single curl command string (already extracted from a bash block).
     */
    private String normalizeSingleCurl(String curl) {
        String normalized = curl.trim().replaceFirst("^curl\\s+", "");
        normalized = normalized.replaceAll("\\\\\\s*\\n\\s*", " ");
        int pipeJq = normalized.indexOf("| jq");
        if (pipeJq >= 0) {
            normalized = normalized.substring(0, pipeJq);
        }
        normalized = normalized.replaceAll("(?m)^\\s*#.*$", "");
        normalized = normalized.replaceAll("\\s+", " ");
        return normalized.trim();
    }

    /**
     * Extracts the first curl command from a multi-line bash block.
     * Handles blocks with comment lines, variable assignments, and multiple commands.
     * Tracks both line continuations (\) and unclosed single quotes for multi-line bodies.
     */
    private String extractFirstCurlCommand(String bash) {
        String[] lines = bash.split("\n");
        StringBuilder curlCmd = new StringBuilder();
        boolean inCurl = false;
        boolean quoteOpen = false;

        for (String line : lines) {
            String trimmed = line.trim();

            if (!inCurl) {
                if (trimmed.startsWith("curl")) {
                    inCurl = true;
                } else {
                    continue; // Skip non-curl lines (comments, variables, etc.)
                }
            }

            curlCmd.append(line).append("\n");

            // Track single-quote parity across lines (for multi-line -d bodies)
            for (char c : trimmed.toCharArray()) {
                if (c == '\'') quoteOpen = !quoteOpen;
            }

            // Command is complete when no unclosed quotes and no line continuation
            if (!quoteOpen && !trimmed.endsWith("\\")) {
                break;
            }
        }

        if (curlCmd.length() == 0) {
            // No curl found; return original (will likely fail on URL extraction)
            return bash;
        }

        return curlCmd.toString().trim();
    }

    private String extractUrl(String curl) {
        // URL is typically the first argument that starts with http
        Pattern pattern = Pattern.compile("(https?://[^\\s\"']+)");
        Matcher matcher = pattern.matcher(curl);

        if (matcher.find()) {
            return matcher.group(1);
        }

        throw new IllegalArgumentException("No URL found in curl command: " + curl);
    }

    private String extractMethod(String curl) {
        // Look for -X METHOD, --request METHOD, or combined flags like -sSfX DELETE
        Pattern pattern = Pattern.compile("-[a-zA-Z]*X\\s+([A-Z]+)|--request\\s+([A-Z]+)");
        Matcher matcher = pattern.matcher(curl);

        if (matcher.find()) {
            String method = matcher.group(1);
            if (method == null) {
                method = matcher.group(2);
            }
            return method;
        }

        // Default to POST if there's a body or form data, otherwise GET
        if (curl.contains("-d ")
                || curl.contains("--data")
                || curl.contains("-F ")
                || curl.contains("--form ")) {
            return "POST";
        }

        return "GET";
    }

    private Map<String, String> extractHeaders(String curl) {
        Map<String, String> headers = new HashMap<>();

        // Match -H "Header: Value" or --header "Header: Value"
        Pattern pattern = Pattern.compile("(?:-H|--header)\\s+['\"]([^:]+):\\s*([^'\"]+)['\"]");
        Matcher matcher = pattern.matcher(curl);

        while (matcher.find()) {
            String headerName = matcher.group(1).trim();
            String headerValue = matcher.group(2).trim();
            headers.put(headerName, headerValue);
        }

        return headers;
    }

    private String extractBody(String curl) {
        // Match -d 'body' or -d "body" or --data 'body' or --data "body"
        // Handle escaped quotes within the body (e.g., -d '"text'\''more"')

        // First try to match the -d or --data flag position
        Pattern flagPattern = Pattern.compile("(?:-d|--data)\\s+");
        Matcher flagMatcher = flagPattern.matcher(curl);

        if (!flagMatcher.find()) {
            return null;
        }

        int start = flagMatcher.end();
        if (start >= curl.length()) {
            return null;
        }

        // Determine the quote character
        char quoteChar = curl.charAt(start);
        if (quoteChar != '\'' && quoteChar != '"') {
            // No quotes - match until next space or end
            int end = curl.indexOf(' ', start);
            if (end == -1) end = curl.length();
            return curl.substring(start, end);
        }

        // Find the matching closing quote (handling escaped quotes)
        StringBuilder body = new StringBuilder();
        int i = start + 1; // Skip opening quote

        while (i < curl.length()) {
            char ch = curl.charAt(i);

            if (ch == '\\' && i + 1 < curl.length()) {
                // Escaped character
                i++;
                char nextCh = curl.charAt(i);
                if (nextCh == quoteChar || nextCh == '\\') {
                    body.append(nextCh);
                } else {
                    body.append('\\').append(nextCh);
                }
                i++;
            } else if (ch == quoteChar) {
                // Check for '\'' pattern (escaped quote in shell)
                if (quoteChar == '\''
                        && i + 3 < curl.length()
                        && curl.substring(i, i + 4).equals("'\\''")) {
                    body.append('\'');
                    i += 4; // Skip the '\'' pattern
                } else {
                    // End of quoted string
                    break;
                }
            } else {
                body.append(ch);
                i++;
            }
        }

        return body.toString();
    }

    /**
     * Extracts -F/--form fields from a curl command.
     * Returns a list of [name, value] pairs where value may start with @ for file uploads.
     */
    private List<String[]> extractFormFields(String curl) {
        List<String[]> fields = new ArrayList<>();
        Pattern pattern = Pattern.compile("(?:-F|--form)\\s+['\"]([^=]+)=([^'\"]+)['\"]");
        Matcher matcher = pattern.matcher(curl);
        while (matcher.find()) {
            fields.add(new String[] {matcher.group(1), matcher.group(2)});
        }
        return fields;
    }

    /**
     * Builds a multipart/form-data request body from form fields.
     * Handles file uploads (values starting with @) by reading the file.
     */
    private byte[] buildMultipartBody(List<String[]> fields, String boundary) throws Exception {
        ByteArrayOutputStream baos = new ByteArrayOutputStream();
        for (String[] field : fields) {
            String name = field[0];
            String value = replaceEnvironmentVariables(field[1]);
            baos.write(("--" + boundary + "\r\n").getBytes(StandardCharsets.UTF_8));
            if (value.startsWith("@")) {
                Path filePath = Path.of(value.substring(1));
                String filename = filePath.getFileName().toString();
                baos.write(
                        ("Content-Disposition: form-data; name=\""
                                        + name
                                        + "\"; filename=\""
                                        + filename
                                        + "\"\r\n")
                                .getBytes(StandardCharsets.UTF_8));
                String contentType = java.net.URLConnection.guessContentTypeFromName(filename);
                if (contentType == null) {
                    contentType = "application/octet-stream";
                }
                baos.write(
                        ("Content-Type: " + contentType + "\r\n\r\n")
                                .getBytes(StandardCharsets.UTF_8));
                baos.write(Files.readAllBytes(filePath));
            } else {
                baos.write(
                        ("Content-Disposition: form-data; name=\"" + name + "\"\r\n\r\n")
                                .getBytes(StandardCharsets.UTF_8));
                baos.write(value.getBytes(StandardCharsets.UTF_8));
            }
            baos.write("\r\n".getBytes(StandardCharsets.UTF_8));
        }
        baos.write(("--" + boundary + "--\r\n").getBytes(StandardCharsets.UTF_8));
        return baos.toByteArray();
    }

    private String replaceEnvironmentVariables(String text) {
        // Replace ${VAR_NAME} and $(command) patterns
        Pattern envPattern = Pattern.compile("\\$\\{([^}]+)\\}");
        Matcher matcher = envPattern.matcher(text);

        StringBuffer result = new StringBuffer();
        while (matcher.find()) {
            String varName = matcher.group(1);
            String value = environment.getOrDefault(varName, "");
            matcher.appendReplacement(result, Matcher.quoteReplacement(value));
        }
        matcher.appendTail(result);

        // Handle $(command args...) style command substitutions
        // e.g., $(get-token) → CMD_get-token
        //        $(get-token bob bob) → CMD_get-token_bob_bob (then fallback to CMD_get-token)
        Pattern cmdPattern = Pattern.compile("\\$\\(([^)]+)\\)");
        matcher = cmdPattern.matcher(result.toString());

        result = new StringBuffer();
        while (matcher.find()) {
            String command = matcher.group(1).trim();
            // Try exact match first (with args joined by underscore)
            String envKey = "CMD_" + command.replaceAll("\\s+", "_");
            String value = environment.get(envKey);
            if (value == null) {
                // Fallback: try just the command name without args
                String cmdName = command.split("\\s+")[0];
                value = environment.getOrDefault("CMD_" + cmdName, "");
            }
            matcher.appendReplacement(result, Matcher.quoteReplacement(value));
        }
        matcher.appendTail(result);

        return result.toString();
    }
}
