package io.github.chirino.memory.model;

public class AdminMessageQuery {

    private String afterEntryId;
    private int limit;
    private Channel channel;
    private boolean allForks;

    public String getAfterEntryId() {
        return afterEntryId;
    }

    public void setAfterEntryId(String afterEntryId) {
        this.afterEntryId = afterEntryId;
    }

    public int getLimit() {
        return limit;
    }

    public void setLimit(int limit) {
        this.limit = limit;
    }

    public Channel getChannel() {
        return channel;
    }

    public void setChannel(Channel channel) {
        this.channel = channel;
    }

    public boolean isAllForks() {
        return allForks;
    }

    public void setAllForks(boolean allForks) {
        this.allForks = allForks;
    }
}
