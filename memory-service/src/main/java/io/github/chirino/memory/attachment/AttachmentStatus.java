package io.github.chirino.memory.attachment;

public enum AttachmentStatus {
    UPLOADING,
    DOWNLOADING,
    READY,
    FAILED;

    public String value() {
        return name().toLowerCase();
    }

    public static AttachmentStatus fromValue(String value) {
        if (value == null) {
            return READY;
        }
        return valueOf(value.toUpperCase());
    }
}
