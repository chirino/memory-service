package io.github.chirino.memory.api.dto;

import java.util.Collections;
import java.util.List;

public class SyncResult {

    private Long epoch;
    private boolean noOp;
    private boolean epochIncremented;
    private List<EntryDto> entries = Collections.emptyList();

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

    public List<EntryDto> getEntries() {
        return entries;
    }

    public void setEntries(List<EntryDto> entries) {
        this.entries = entries != null ? entries : Collections.emptyList();
    }
}
