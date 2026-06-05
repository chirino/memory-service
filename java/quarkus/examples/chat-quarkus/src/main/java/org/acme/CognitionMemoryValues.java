package org.acme;

import io.github.chirino.memory.client.model.MemoryItem;
import jakarta.ws.rs.BadRequestException;
import jakarta.ws.rs.WebApplicationException;
import java.util.ArrayList;
import java.util.LinkedHashSet;
import java.util.List;
import java.util.Map;
import java.util.Objects;

final class CognitionMemoryValues {

    private CognitionMemoryValues() {}

    static String stringValue(Map<String, Object> value, String key) {
        Object item = value == null ? null : value.get(key);
        return item instanceof String text && !text.isBlank() ? text : null;
    }

    static String supportedContent(MemoryItem item) {
        if (item == null) {
            return null;
        }
        return stringValue(item.getValue(), "content");
    }

    static boolean namespaceEndsWith(List<String> namespace, String finalSegment) {
        return namespace != null
                && !namespace.isEmpty()
                && Objects.equals(namespace.get(namespace.size() - 1), finalSegment);
    }

    static boolean isHttpNotFound(Throwable throwable) {
        Throwable cursor = throwable;
        while (cursor != null) {
            if (cursor instanceof WebApplicationException webApplicationException
                    && webApplicationException.getResponse() != null
                    && webApplicationException.getResponse().getStatus() == 404) {
                return true;
            }
            cursor = cursor.getCause();
        }
        return false;
    }

    static List<String> normalizeInputs(List<?> inputs, int maxItems, int maxItemChars) {
        if (inputs == null) {
            throw new BadRequestException("Request body must be a JSON array of strings");
        }
        LinkedHashSet<String> accepted = new LinkedHashSet<>();
        for (Object input : inputs) {
            if (!(input instanceof String raw)) {
                throw new BadRequestException("Profile inputs must be strings");
            }
            String normalized = normalizeInput(raw);
            if (normalized.isEmpty()) {
                continue;
            }
            if (normalized.length() > maxItemChars) {
                throw new BadRequestException(
                        "Profile input exceeds " + maxItemChars + " characters");
            }
            accepted.add(normalized);
            if (accepted.size() > maxItems) {
                throw new BadRequestException("Too many profile inputs; maximum is " + maxItems);
            }
        }
        return new ArrayList<>(accepted);
    }

    private static String normalizeInput(String input) {
        return input == null ? "" : input.trim().replaceAll("\\s+", " ");
    }
}
