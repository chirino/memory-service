package memory.service.io.github.chirino.dataencryption;

import com.google.protobuf.ByteString;
import com.google.protobuf.CodedInputStream;
import com.google.protobuf.CodedOutputStream;
import java.io.ByteArrayOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.nio.ByteBuffer;

/**
 * Binary prefix for encrypted payloads. Wire format:
 *
 * <pre>
 * [4 bytes magic: 0x4D534548]       // "MSEH" — Memory Service Encryption Header
 * [protobuf uint32 varint: header length]
 * [N bytes: protobuf-encoded EncryptionHeader message]
 * </pre>
 *
 * Followed immediately by the raw ciphertext payload.
 *
 * <p>Protobuf schema (hand-coded, no generated classes):
 *
 * <pre>
 * message EncryptionHeader {
 *   uint32 version    = 1;
 *   string provider_id = 2;
 *   bytes  iv         = 3;  // 12 bytes for AES-GCM; empty for plain provider
 * }
 * </pre>
 */
public final class EncryptionHeader {

    /** Four-byte magic identifying an MSEH-prefixed payload: ASCII "MSEH". */
    public static final int MAGIC = 0x4D534548;

    private final int version;
    private final String providerId;
    private final byte[] iv;

    public EncryptionHeader(int version, String providerId, byte[] iv) {
        this.version = version;
        this.providerId = providerId;
        this.iv = iv;
    }

    public int getVersion() {
        return version;
    }

    public String getProviderId() {
        return providerId;
    }

    public byte[] getIv() {
        return iv;
    }

    /**
     * Read {@code [magic][varint length][proto]} from the start of {@code is}. Reads exactly the
     * bytes needed — does not buffer beyond the header.
     *
     * @throws IOException if the stream is too short, the magic is wrong, or the proto is invalid
     */
    public static EncryptionHeader read(InputStream is) throws IOException {
        // 1. Read and verify 4-byte magic
        byte[] magicBytes = is.readNBytes(4);
        if (magicBytes.length < 4) {
            throw new IOException("Unexpected end of stream reading MSEH magic");
        }
        int magic = ByteBuffer.wrap(magicBytes).getInt();
        if (magic != MAGIC) {
            throw new IOException(
                    "Invalid magic bytes (0x"
                            + Integer.toHexString(magic).toUpperCase()
                            + "); not an MSEH encrypted payload");
        }

        // 2. Read protobuf varint32: header length (byte-by-byte, no buffering)
        int headerLen = readVarint32(is);

        // 3. Read exactly headerLen bytes of protobuf message
        byte[] protoBytes = is.readNBytes(headerLen);
        if (protoBytes.length < headerLen) {
            throw new IOException("Unexpected end of stream reading EncryptionHeader proto");
        }

        // 4. Parse proto fields
        CodedInputStream coded = CodedInputStream.newInstance(protoBytes);
        int version = 0;
        String providerId = "";
        byte[] iv = new byte[0];

        while (!coded.isAtEnd()) {
            int tag = coded.readTag();
            if (tag == 0) break;
            int fieldNumber = tag >>> 3;
            switch (fieldNumber) {
                case 1 -> version = coded.readUInt32();
                case 2 -> providerId = coded.readString();
                case 3 -> iv = coded.readBytes().toByteArray();
                default -> coded.skipField(tag);
            }
        }

        return new EncryptionHeader(version, providerId, iv);
    }

    /**
     * Read {@code [varint length][proto]} from {@code is}, assuming the 4-byte magic has already
     * been consumed. Used by {@link DataEncryptionService} after peeking the magic bytes.
     *
     * @throws IOException if the stream is too short or the proto is invalid
     */
    static EncryptionHeader readAfterMagic(InputStream is) throws IOException {
        // 1. Read protobuf varint32: header length
        int headerLen = readVarint32(is);

        // 2. Read exactly headerLen bytes of protobuf message
        byte[] protoBytes = is.readNBytes(headerLen);
        if (protoBytes.length < headerLen) {
            throw new IOException("Unexpected end of stream reading EncryptionHeader proto");
        }

        // 3. Parse proto fields
        CodedInputStream coded = CodedInputStream.newInstance(protoBytes);
        int version = 0;
        String providerId = "";
        byte[] iv = new byte[0];

        while (!coded.isAtEnd()) {
            int tag = coded.readTag();
            if (tag == 0) break;
            int fieldNumber = tag >>> 3;
            switch (fieldNumber) {
                case 1 -> version = coded.readUInt32();
                case 2 -> providerId = coded.readString();
                case 3 -> iv = coded.readBytes().toByteArray();
                default -> coded.skipField(tag);
            }
        }

        return new EncryptionHeader(version, providerId, iv);
    }

    /**
     * Write {@code [magic][varint length][proto]} to {@code os}. The caller is responsible for
     * writing the ciphertext payload immediately after.
     */
    public void write(OutputStream os) throws IOException {
        // Encode proto message
        ByteArrayOutputStream protoOut = new ByteArrayOutputStream();
        CodedOutputStream coded = CodedOutputStream.newInstance(protoOut);
        coded.writeUInt32(1, version);
        coded.writeString(2, providerId);
        coded.writeBytes(3, ByteString.copyFrom(iv));
        coded.flush();
        byte[] protoBytes = protoOut.toByteArray();

        // Write 4-byte magic
        os.write(ByteBuffer.allocate(4).putInt(MAGIC).array());

        // Write varint-encoded proto length
        writeVarint32(os, protoBytes.length);

        // Write proto bytes
        os.write(protoBytes);
    }

    /** Read a protobuf varint32 from {@code is}, one byte at a time (no buffering). */
    private static int readVarint32(InputStream is) throws IOException {
        int result = 0;
        int shift = 0;
        while (shift < 32) {
            int b = is.read();
            if (b == -1) {
                throw new IOException("Unexpected end of stream reading varint");
            }
            result |= (b & 0x7F) << shift;
            if ((b & 0x80) == 0) {
                return result;
            }
            shift += 7;
        }
        throw new IOException("Malformed varint: too many bytes");
    }

    /** Write {@code value} as a protobuf varint32 to {@code os}. */
    private static void writeVarint32(OutputStream os, int value) throws IOException {
        while (true) {
            if ((value & ~0x7F) == 0) {
                os.write(value);
                return;
            }
            os.write((value & 0x7F) | 0x80);
            value >>>= 7;
        }
    }
}
