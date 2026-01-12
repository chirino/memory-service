package io.github.chirino.memory.store;

import java.util.Objects;

public final class MemoryEpochFilter {

    public enum Mode {
        LATEST,
        ALL,
        EPOCH
    }

    private final Mode mode;
    private final Long epoch;

    private MemoryEpochFilter(Mode mode, Long epoch) {
        this.mode = mode;
        this.epoch = epoch;
    }

    public static MemoryEpochFilter latest() {
        return new MemoryEpochFilter(Mode.LATEST, null);
    }

    public static MemoryEpochFilter all() {
        return new MemoryEpochFilter(Mode.ALL, null);
    }

    public static MemoryEpochFilter epoch(long epoch) {
        return new MemoryEpochFilter(Mode.EPOCH, epoch);
    }

    public Mode getMode() {
        return mode;
    }

    public Long getEpoch() {
        return epoch;
    }

    public static MemoryEpochFilter parse(String raw) {
        if (raw == null || raw.isBlank()) {
            return latest();
        }
        String value = raw.trim().toLowerCase();
        if ("latest".equals(value)) {
            return latest();
        }
        if ("all".equals(value)) {
            return all();
        }
        try {
            long epoch = Long.parseLong(value);
            return epoch(epoch);
        } catch (NumberFormatException e) {
            throw new IllegalArgumentException(
                    "epoch filter must be 'latest', 'all', or an epoch number");
        }
    }

    @Override
    public String toString() {
        return "MemoryEpochFilter{" + "mode=" + mode + ", epoch=" + epoch + '}';
    }

    @Override
    public int hashCode() {
        return Objects.hash(mode, epoch);
    }

    @Override
    public boolean equals(Object obj) {
        if (this == obj) {
            return true;
        }
        if (obj == null || getClass() != obj.getClass()) {
            return false;
        }
        MemoryEpochFilter other = (MemoryEpochFilter) obj;
        return mode == other.mode && Objects.equals(epoch, other.epoch);
    }
}
