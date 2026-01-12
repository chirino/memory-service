package io.github.chirino.memory.api.dto;

import java.util.Collections;
import java.util.List;

public class SyncResult {

    private Long memoryEpoch;
    private boolean noOp;
    private boolean epochIncremented;
    private List<MessageDto> messages = Collections.emptyList();

    public Long getMemoryEpoch() {
        return memoryEpoch;
    }

    public void setMemoryEpoch(Long memoryEpoch) {
        this.memoryEpoch = memoryEpoch;
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
