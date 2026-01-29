package io.github.chirino.memory.grpc;

import com.google.protobuf.ByteString;
import java.nio.ByteBuffer;
import java.util.UUID;

/**
 * Utility class for converting between Java UUID and gRPC bytes representation.
 *
 * <p>UUIDs are represented as 16-byte big-endian binary values in the gRPC API. The most
 * significant 64 bits come first, followed by the least significant 64 bits.
 */
public final class UuidUtils {

    private static final int UUID_BYTE_LENGTH = 16;

    private UuidUtils() {}

    /**
     * Converts a UUID to a 16-byte big-endian byte array.
     *
     * @param uuid the UUID to convert
     * @return 16-byte array representation
     */
    public static byte[] toBytes(UUID uuid) {
        if (uuid == null) {
            return new byte[0];
        }
        ByteBuffer buffer = ByteBuffer.allocate(UUID_BYTE_LENGTH);
        buffer.putLong(uuid.getMostSignificantBits());
        buffer.putLong(uuid.getLeastSignificantBits());
        return buffer.array();
    }

    /**
     * Converts a 16-byte big-endian byte array to a UUID.
     *
     * @param bytes the byte array to convert
     * @return the UUID, or null if bytes is null or empty
     * @throws IllegalArgumentException if bytes is not 16 bytes
     */
    public static UUID fromBytes(byte[] bytes) {
        if (bytes == null || bytes.length == 0) {
            return null;
        }
        if (bytes.length != UUID_BYTE_LENGTH) {
            throw new IllegalArgumentException("UUID bytes must be 16 bytes, got " + bytes.length);
        }
        ByteBuffer buffer = ByteBuffer.wrap(bytes);
        long mostSig = buffer.getLong();
        long leastSig = buffer.getLong();
        return new UUID(mostSig, leastSig);
    }

    /**
     * Converts a UUID to a gRPC ByteString.
     *
     * @param uuid the UUID to convert
     * @return ByteString representation (empty if uuid is null)
     */
    public static ByteString toByteString(UUID uuid) {
        if (uuid == null) {
            return ByteString.EMPTY;
        }
        return ByteString.copyFrom(toBytes(uuid));
    }

    /**
     * Converts a gRPC ByteString to a UUID.
     *
     * @param bytes the ByteString to convert
     * @return the UUID, or null if bytes is null or empty
     * @throws IllegalArgumentException if bytes is not 16 bytes
     */
    public static UUID fromByteString(ByteString bytes) {
        if (bytes == null || bytes.isEmpty()) {
            return null;
        }
        return fromBytes(bytes.toByteArray());
    }

    /**
     * Converts a UUID string to a gRPC ByteString.
     *
     * @param uuidString the UUID string to convert
     * @return ByteString representation (empty if uuidString is null or empty)
     * @throws IllegalArgumentException if the string is not a valid UUID format
     */
    public static ByteString stringToByteString(String uuidString) {
        if (uuidString == null || uuidString.isEmpty()) {
            return ByteString.EMPTY;
        }
        return toByteString(UUID.fromString(uuidString));
    }

    /**
     * Converts a gRPC ByteString to a UUID string.
     *
     * @param bytes the ByteString to convert
     * @return the UUID string, or null if bytes is null or empty
     * @throws IllegalArgumentException if bytes is not 16 bytes
     */
    public static String byteStringToString(ByteString bytes) {
        UUID uuid = fromByteString(bytes);
        return uuid == null ? null : uuid.toString();
    }
}
