package io.github.chirino.memory.api.dto;

import java.util.Collections;
import java.util.List;

public class SyncResult {

    private Long epoch;
    private boolean noOp;
    private boolean epochIncremented;
    private List<MessageDto> messages = Collections.emptyList();

    public Long getEpoch() {
        return epoch;
    }

    public void setEpoch(Long epoch) {
        this.epoch = epoch;
    }

    public boolean isNoOp() {
        return noOp;
    }

    public void setNoOp(boolean noOp) {
        this.noOp = noOp;
    }

    public boolean isEpochIncremented() {
        return epochIncremented;
    }

    public void setEpochIncremented(boolean epochIncremented) {
        this.epochIncremented = epochIncremented;
    }

    public List<MessageDto> getMessages() {
        return messages;
    }

    public void setMessages(List<MessageDto> messages) {
        this.messages = messages != null ? messages : Collections.emptyList();
    }
}
