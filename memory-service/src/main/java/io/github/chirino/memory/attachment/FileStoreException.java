package io.github.chirino.memory.attachment;

import java.util.Map;

/**
 * Exception thrown by FileStore implementations. Carries an error code, HTTP status, and optional
 * details map so the REST layer can forward errors to API users without branching on types.
 *
 * <p>Well-known codes are provided as constants, but implementations may use any string code.
 */
public class FileStoreException extends RuntimeException {

    /** File exceeds the maximum allowed size. Suggested HTTP status: 413. */
    public static final String FILE_TOO_LARGE = "file_too_large";

    /** Generic storage backend error. Suggested HTTP status: 500. */
    public static final String STORAGE_ERROR = "storage_error";

    private final String code;
    private final int httpStatus;
    private final Map<String, Object> details;

    public FileStoreException(String code, int httpStatus, String message) {
        this(code, httpStatus, message, Map.of(), null);
    }

    public FileStoreException(
            String code, int httpStatus, String message, Map<String, Object> details) {
        this(code, httpStatus, message, details, null);
    }

    public FileStoreException(
            String code,
            int httpStatus,
            String message,
            Map<String, Object> details,
            Throwable cause) {
        super(message, cause);
        this.code = code;
        this.httpStatus = httpStatus;
        this.details = details != null ? details : Map.of();
    }

    /** Convenience constructor for file-too-large errors. */
    public FileStoreException(long maxBytes, long actualBytes) {
        this(
                FILE_TOO_LARGE,
                413,
                "File too large: "
                        + actualBytes
                        + " bytes exceeds maximum of "
                        + maxBytes
                        + " bytes",
                Map.of("maxBytes", maxBytes, "actualBytes", actualBytes));
    }

    /** Convenience constructor for storage errors. */
    public static FileStoreException storageError(String message, Throwable cause) {
        return new FileStoreException(STORAGE_ERROR, 500, message, Map.of(), cause);
    }

    public String getCode() {
        return code;
    }

    public int getHttpStatus() {
        return httpStatus;
    }

    public Map<String, Object> getDetails() {
        return details;
    }
}
