package memory.service.io.github.chirino.dataencryption;

public interface KeyEncryptionService {

    GeneratedDek generateDek();

    byte[] decryptDek(byte[] encryptedDek);
}
