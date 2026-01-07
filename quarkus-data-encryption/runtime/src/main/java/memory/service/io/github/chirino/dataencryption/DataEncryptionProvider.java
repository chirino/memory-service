package memory.service.io.github.chirino.dataencryption;

public interface DataEncryptionProvider {

    String id();

    byte[] encrypt(byte[] plaintext);

    byte[] decrypt(byte[] ciphertext) throws DecryptionFailedException;
}
