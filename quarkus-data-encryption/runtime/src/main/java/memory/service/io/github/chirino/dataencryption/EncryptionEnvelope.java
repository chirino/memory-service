package memory.service.io.github.chirino.dataencryption;

import com.google.protobuf.CodedInputStream;
import com.google.protobuf.CodedOutputStream;
import com.google.protobuf.InvalidProtocolBufferException;
import java.io.ByteArrayInputStream;
import java.io.ByteArrayOutputStream;
import java.io.IOException;

/**
 * Protobuf-based encryption envelope.
 *
 * syntax = "proto3";
 * package data.encryption;
 *
 * message EncryptionEnvelope {
 *   uint32 version = 1;
 *   string provider_id = 2;
 *   bytes payload = 3;
 * }
 */
public final class EncryptionEnvelope {

    private static final int CURRENT_VERSION = 1;

    private final int version;
    private final String providerId;
    private final byte[] payload;

    public EncryptionEnvelope(int version, String providerId, byte[] payload) {
        this.version = version;
        this.providerId = providerId;
        this.payload = payload;
    }

    public int getVersion() {
        return version;
    }

    public String getProviderId() {
        return providerId;
    }

    public byte[] getPayload() {
        return payload;
    }

    public byte[] toBytes() {
        try {
            ByteArrayOutputStream baos = new ByteArrayOutputStream();
            CodedOutputStream out = CodedOutputStream.newInstance(baos);

            out.writeUInt32(1, version);
            out.writeString(2, providerId);
            out.writeBytes(3, com.google.protobuf.ByteString.copyFrom(payload));
            out.flush();

            return baos.toByteArray();
        } catch (IOException e) {
            throw new IllegalStateException("Failed to serialize encryption envelope", e);
        }
    }

    public static EncryptionEnvelope fromBytes(byte[] bytes) throws InvalidProtocolBufferException {
        try {
            CodedInputStream in = CodedInputStream.newInstance(new ByteArrayInputStream(bytes));

            int version = 0;
            String providerId = null;
            byte[] payload = null;

            while (!in.isAtEnd()) {
                int tag = in.readTag();
                if (tag == 0) {
                    break;
                }
                int fieldNumber = tag >>> 3;
                switch (fieldNumber) {
                    case 1:
                        version = in.readUInt32();
                        break;
                    case 2:
                        providerId = in.readString();
                        break;
                    case 3:
                        payload = in.readBytes().toByteArray();
                        break;
                    default:
                        in.skipField(tag);
                }
            }

            if (providerId == null || payload == null) {
                throw new InvalidProtocolBufferException("Invalid encryption envelope data");
            }

            return new EncryptionEnvelope(version, providerId, payload);
        } catch (IOException e) {
            throw new InvalidProtocolBufferException(e);
        }
    }

    public static EncryptionEnvelope wrap(String providerId, byte[] payload) {
        return new EncryptionEnvelope(CURRENT_VERSION, providerId, payload);
    }
}
