package io.github.chirino.memory.api.dto;

public class SyncResult {

    private Long epoch;
    private boolean noOp;
    private boolean epochIncremented;
    private EntryDto entry;

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

    public EntryDto getEntry() {
        return entry;
    }

    public void setEntry(EntryDto entry) {
        this.entry = entry;
    }
}
