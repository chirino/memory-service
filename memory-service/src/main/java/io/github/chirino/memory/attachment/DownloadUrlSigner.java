package io.github.chirino.memory.attachment;

import io.github.chirino.memory.config.AttachmentConfig;
import jakarta.annotation.PostConstruct;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.security.SecureRandom;
import java.time.Duration;
import java.time.Instant;
import java.util.Base64;
import java.util.Optional;
import javax.crypto.Mac;
import javax.crypto.spec.SecretKeySpec;
import org.jboss.logging.Logger;

@ApplicationScoped
public class DownloadUrlSigner {

    private static final Logger LOG = Logger.getLogger(DownloadUrlSigner.class);
    private static final String HMAC_ALGORITHM = "HmacSHA256";

    @Inject AttachmentConfig config;

    private SecretKeySpec secretKey;

    @PostConstruct
    void init() {
        Optional<String> configuredSecret = config.getDownloadUrlSecret();
        byte[] keyBytes;
        if (configuredSecret.isPresent() && !configuredSecret.get().isBlank()) {
            keyBytes = configuredSecret.get().getBytes(StandardCharsets.UTF_8);
            LOG.info("Using configured download URL signing secret");
        } else {
            keyBytes = new byte[32]; // 256-bit key
            new SecureRandom().nextBytes(keyBytes);
            LOG.info(
                    "No download URL secret configured; generated ephemeral key (URLs won't"
                            + " survive restart)");
        }
        secretKey = new SecretKeySpec(keyBytes, HMAC_ALGORITHM);
    }

    public record SignedDownloadClaim(String attachmentId, Instant expiresAt) {}

    public String createToken(String attachmentId, Duration expiry) {
        long expiryEpoch = Instant.now().plus(expiry).getEpochSecond();
        String payload = attachmentId + "." + expiryEpoch;
        byte[] signature = computeHmac(payload);
        String token =
                payload + "." + Base64.getUrlEncoder().withoutPadding().encodeToString(signature);
        return Base64.getUrlEncoder()
                .withoutPadding()
                .encodeToString(token.getBytes(StandardCharsets.UTF_8));
    }

    public Optional<SignedDownloadClaim> verifyToken(String token) {
        try {
            String decoded =
                    new String(Base64.getUrlDecoder().decode(token), StandardCharsets.UTF_8);
            // Format: attachmentId.expiryEpoch.signature
            int lastDot = decoded.lastIndexOf('.');
            if (lastDot < 0) return Optional.empty();

            String payload = decoded.substring(0, lastDot);
            String signatureB64 = decoded.substring(lastDot + 1);

            // Verify HMAC
            byte[] expectedSignature = computeHmac(payload);
            byte[] actualSignature = Base64.getUrlDecoder().decode(signatureB64);
            if (!MessageDigest.isEqual(expectedSignature, actualSignature)) {
                return Optional.empty();
            }

            // Parse payload
            int dot = payload.indexOf('.');
            if (dot < 0) return Optional.empty();

            String attachmentId = payload.substring(0, dot);
            long expiryEpoch = Long.parseLong(payload.substring(dot + 1));
            Instant expiresAt = Instant.ofEpochSecond(expiryEpoch);

            // Check expiry
            if (Instant.now().isAfter(expiresAt)) {
                return Optional.empty();
            }

            return Optional.of(new SignedDownloadClaim(attachmentId, expiresAt));
        } catch (Exception e) {
            LOG.debugf("Failed to verify download token: %s", e.getMessage());
            return Optional.empty();
        }
    }

    private byte[] computeHmac(String data) {
        try {
            Mac mac = Mac.getInstance(HMAC_ALGORITHM);
            mac.init(secretKey);
            return mac.doFinal(data.getBytes(StandardCharsets.UTF_8));
        } catch (Exception e) {
            throw new RuntimeException("HMAC computation failed", e);
        }
    }
}
