package io.github.chirino.memory.api;

import jakarta.ws.rs.WebApplicationException;
import jakarta.ws.rs.core.Response;

public enum ConversationListMode {
    ALL("all"),
    ROOTS("roots"),
    LATEST_FORK("latest-fork");

    private final String queryValue;

    ConversationListMode(String queryValue) {
        this.queryValue = queryValue;
    }

    public String getQueryValue() {
        return queryValue;
    }

    public static ConversationListMode fromQuery(String value) {
        if (value == null || value.isBlank()) {
            return LATEST_FORK;
        }
        for (ConversationListMode mode : values()) {
            if (mode.queryValue.equals(value)) {
                return mode;
            }
        }
        throw new WebApplicationException(
                "Invalid mode. Expected one of: all, roots, latest-fork",
                Response.Status.BAD_REQUEST);
    }
}
