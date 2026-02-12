package io.github.chirino.memory.model;

public class AdminAttachmentQuery {

    private String userId;
    private String entryId;
    private AttachmentStatus status = AttachmentStatus.ALL;
    private String after;
    private int limit = 50;

    public String getUserId() {
        return userId;
    }

    public void setUserId(String userId) {
        this.userId = userId;
    }

    public String getEntryId() {
        return entryId;
    }

    public void setEntryId(String entryId) {
        this.entryId = entryId;
    }

    public AttachmentStatus getStatus() {
        return status;
    }

    public void setStatus(AttachmentStatus status) {
        this.status = status != null ? status : AttachmentStatus.ALL;
    }

    public String getAfter() {
        return after;
    }

    public void setAfter(String after) {
        this.after = after;
    }

    public int getLimit() {
        return limit;
    }

    public void setLimit(int limit) {
        this.limit = Math.min(Math.max(limit, 1), 200);
    }

    public enum AttachmentStatus {
        LINKED,
        UNLINKED,
        EXPIRED,
        ALL;

        public static AttachmentStatus fromString(String value) {
            if (value == null || value.isBlank()) {
                return ALL;
            }
            return valueOf(value.toUpperCase());
        }
    }
}
