package io.github.chirino.memory.model;

import io.github.chirino.memory.api.ConversationListMode;
import java.time.OffsetDateTime;

public class AdminConversationQuery {

    private ConversationListMode mode = ConversationListMode.LATEST_FORK;
    private String userId;
    private boolean includeDeleted;
    private boolean onlyDeleted;
    private OffsetDateTime deletedAfter;
    private OffsetDateTime deletedBefore;
    private String afterCursor;
    private int limit;

    public ConversationListMode getMode() {
        return mode;
    }

    public void setMode(ConversationListMode mode) {
        this.mode = mode != null ? mode : ConversationListMode.LATEST_FORK;
    }

    public String getUserId() {
        return userId;
    }

    public void setUserId(String userId) {
        this.userId = userId;
    }

    public boolean isIncludeDeleted() {
        return includeDeleted;
    }

    public void setIncludeDeleted(boolean includeDeleted) {
        this.includeDeleted = includeDeleted;
    }

    public boolean isOnlyDeleted() {
        return onlyDeleted;
    }

    public void setOnlyDeleted(boolean onlyDeleted) {
        this.onlyDeleted = onlyDeleted;
    }

    public OffsetDateTime getDeletedAfter() {
        return deletedAfter;
    }

    public void setDeletedAfter(OffsetDateTime deletedAfter) {
        this.deletedAfter = deletedAfter;
    }

    public OffsetDateTime getDeletedBefore() {
        return deletedBefore;
    }

    public void setDeletedBefore(OffsetDateTime deletedBefore) {
        this.deletedBefore = deletedBefore;
    }

    public String getAfterCursor() {
        return afterCursor;
    }

    public void setAfterCursor(String afterCursor) {
        this.afterCursor = afterCursor;
    }

    public int getLimit() {
        return limit;
    }

    public void setLimit(int limit) {
        this.limit = limit;
    }
}
