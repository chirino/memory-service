package io.github.chirino.memory.runtime;

import com.fasterxml.jackson.databind.JavaType;
import com.fasterxml.jackson.databind.ObjectMapper;
import jakarta.ws.rs.WebApplicationException;
import jakarta.ws.rs.core.Response;
import java.io.BufferedInputStream;
import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.net.StandardProtocolFamily;
import java.net.UnixDomainSocketAddress;
import java.nio.channels.Channels;
import java.nio.channels.SocketChannel;
import java.nio.charset.StandardCharsets;
import java.util.LinkedHashMap;
import java.util.Map;
import java.util.Objects;
import java.util.StringJoiner;

public final class UnixSocketHttpClient {

    private final UnixDomainSocketAddress address;
    private final ObjectMapper objectMapper;
    private final String apiKey;
    private final String bearerToken;

    public UnixSocketHttpClient(
            String unixSocketPath, ObjectMapper objectMapper, String apiKey, String bearerToken) {
        this.address = UnixDomainSocketAddress.of(unixSocketPath);
        this.objectMapper = objectMapper;
        this.apiKey = apiKey;
        this.bearerToken = bearerToken;
    }

    public HttpResponseData exchange(
            String method, String path, Map<String, ?> queryParams, Object body)
            throws IOException {
        byte[] bodyBytes = body == null ? new byte[0] : objectMapper.writeValueAsBytes(body);
        return exchange(
                method,
                path,
                queryParams,
                bodyBytes,
                bodyBytes.length > 0 ? "application/json" : null,
                Map.of());
    }

    public HttpResponseData exchange(
            String method,
            String path,
            Map<String, ?> queryParams,
            byte[] bodyBytes,
            String contentType,
            Map<String, String> requestHeaders)
            throws IOException {
        String requestPath = appendQuery(path, queryParams);
        byte[] payload = bodyBytes == null ? new byte[0] : bodyBytes;

        try (SocketChannel channel = SocketChannel.open(StandardProtocolFamily.UNIX)) {
            channel.connect(address);
            var input = new BufferedInputStream(Channels.newInputStream(channel));
            var output = Channels.newOutputStream(channel);

            StringBuilder request = new StringBuilder();
            request.append(method).append(' ').append(requestPath).append(" HTTP/1.1\r\n");
            request.append("Host: localhost\r\n");
            request.append("Accept: application/json\r\n");
            request.append("Connection: close\r\n");
            if (apiKey != null && !apiKey.isBlank()) {
                request.append("X-API-Key: ").append(apiKey).append("\r\n");
            }
            if (bearerToken != null && !bearerToken.isBlank()) {
                request.append("Authorization: Bearer ").append(bearerToken).append("\r\n");
            }
            for (Map.Entry<String, String> entry : requestHeaders.entrySet()) {
                request.append(entry.getKey()).append(": ").append(entry.getValue()).append("\r\n");
            }
            if (payload.length > 0) {
                if (contentType != null && !contentType.isBlank()) {
                    request.append("Content-Type: ").append(contentType).append("\r\n");
                }
                request.append("Content-Length: ").append(payload.length).append("\r\n");
            }
            request.append("\r\n");

            output.write(request.toString().getBytes(StandardCharsets.UTF_8));
            if (payload.length > 0) {
                output.write(payload);
            }
            output.flush();

            String statusLine = readLine(input);
            if (statusLine == null || statusLine.isBlank()) {
                throw new IOException("empty HTTP response from unix socket");
            }
            String[] statusParts = statusLine.split(" ", 3);
            if (statusParts.length < 2) {
                throw new IOException("invalid HTTP status line: " + statusLine);
            }
            int statusCode = Integer.parseInt(statusParts[1]);
            Map<String, String> headers = readHeaders(input);
            byte[] responseBody = readBody(input, headers);
            return new HttpResponseData(statusCode, headers, responseBody);
        }
    }

    public <T> T readJson(HttpResponseData response, JavaType type) throws IOException {
        if (response.body().length == 0) {
            return null;
        }
        return objectMapper.readValue(response.body(), type);
    }

    public Response toJaxrsResponse(HttpResponseData response) {
        Response.ResponseBuilder builder = Response.status(response.statusCode());
        String contentType = response.header("content-type");
        if (contentType != null) {
            builder.type(contentType);
        }
        if (response.body().length > 0) {
            builder.entity(new String(response.body(), StandardCharsets.UTF_8));
        }
        return builder.build();
    }

    public void throwForError(HttpResponseData response) {
        if (response.statusCode() >= 400) {
            throw new WebApplicationException(toJaxrsResponse(response));
        }
    }

    private static String appendQuery(String path, Map<String, ?> queryParams) {
        if (queryParams == null || queryParams.isEmpty()) {
            return path;
        }
        StringJoiner joiner = new StringJoiner("&");
        for (Map.Entry<String, ?> entry : queryParams.entrySet()) {
            Object value = entry.getValue();
            if (value == null) {
                continue;
            }
            joiner.add(urlEncode(entry.getKey()) + "=" + urlEncode(String.valueOf(value)));
        }
        String query = joiner.toString();
        if (query.isEmpty()) {
            return path;
        }
        return path + "?" + query;
    }

    private static String urlEncode(String value) {
        StringBuilder encoded = new StringBuilder();
        for (byte b : value.getBytes(StandardCharsets.UTF_8)) {
            int ch = b & 0xFF;
            if ((ch >= 'a' && ch <= 'z')
                    || (ch >= 'A' && ch <= 'Z')
                    || (ch >= '0' && ch <= '9')
                    || ch == '-'
                    || ch == '_'
                    || ch == '.'
                    || ch == '~') {
                encoded.append((char) ch);
            } else {
                encoded.append('%');
                encoded.append(Character.toUpperCase(Character.forDigit((ch >> 4) & 0xF, 16)));
                encoded.append(Character.toUpperCase(Character.forDigit(ch & 0xF, 16)));
            }
        }
        return encoded.toString();
    }

    private static Map<String, String> readHeaders(BufferedInputStream input) throws IOException {
        Map<String, String> headers = new LinkedHashMap<>();
        for (; ; ) {
            String line = readLine(input);
            if (line == null || line.isEmpty()) {
                return headers;
            }
            int idx = line.indexOf(':');
            if (idx <= 0) {
                continue;
            }
            String name = line.substring(0, idx).trim().toLowerCase();
            String value = line.substring(idx + 1).trim();
            headers.put(name, value);
        }
    }

    private static byte[] readBody(BufferedInputStream input, Map<String, String> headers)
            throws IOException {
        String transferEncoding = headers.getOrDefault("transfer-encoding", "");
        if (transferEncoding.toLowerCase().contains("chunked")) {
            return readChunkedBody(input);
        }
        String contentLength = headers.get("content-length");
        if (contentLength != null && !contentLength.isBlank()) {
            int length = Integer.parseInt(contentLength.trim());
            return input.readNBytes(length);
        }
        return input.readAllBytes();
    }

    private static byte[] readChunkedBody(BufferedInputStream input) throws IOException {
        ByteArrayOutputStream body = new ByteArrayOutputStream();
        for (; ; ) {
            String chunkSizeLine = readLine(input);
            if (chunkSizeLine == null) {
                throw new IOException("unexpected EOF while reading chunked response");
            }
            int semicolon = chunkSizeLine.indexOf(';');
            String rawSize = semicolon >= 0 ? chunkSizeLine.substring(0, semicolon) : chunkSizeLine;
            int chunkSize = Integer.parseInt(rawSize.trim(), 16);
            if (chunkSize == 0) {
                while (true) {
                    String trailer = readLine(input);
                    if (trailer == null || trailer.isEmpty()) {
                        return body.toByteArray();
                    }
                }
            }
            body.write(input.readNBytes(chunkSize));
            readLine(input);
        }
    }

    private static String readLine(BufferedInputStream input) throws IOException {
        ByteArrayOutputStream buffer = new ByteArrayOutputStream();
        int value;
        boolean seenCarriageReturn = false;
        while ((value = input.read()) != -1) {
            if (seenCarriageReturn) {
                if (value == '\n') {
                    return buffer.toString(StandardCharsets.UTF_8);
                }
                buffer.write('\r');
                seenCarriageReturn = false;
            }
            if (value == '\r') {
                seenCarriageReturn = true;
                continue;
            }
            buffer.write(value);
        }
        if (seenCarriageReturn) {
            buffer.write('\r');
        }
        if (buffer.size() == 0) {
            return null;
        }
        return buffer.toString(StandardCharsets.UTF_8);
    }

    public record HttpResponseData(int statusCode, Map<String, String> headers, byte[] body) {
        public String header(String name) {
            return headers.get(Objects.requireNonNull(name).toLowerCase());
        }
    }
}
