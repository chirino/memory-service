package io.github.chirino.memory.model;

public class AdminMessageQuery {

    private String afterMessageId;
    private int limit;
    private MessageChannel channel;
    private boolean includeDeleted;

    public String getAfterMessageId() {
        return afterMessageId;
    }

    public void setAfterMessageId(String afterMessageId) {
        this.afterMessageId = afterMessageId;
    }

    public int getLimit() {
        return limit;
    }

    public void setLimit(int limit) {
        this.limit = limit;
    }

    public MessageChannel getChannel() {
        return channel;
    }

    public void setChannel(MessageChannel channel) {
        this.channel = channel;
    }

    public boolean isIncludeDeleted() {
        return includeDeleted;
    }

    public void setIncludeDeleted(boolean includeDeleted) {
        this.includeDeleted = includeDeleted;
    }
}
