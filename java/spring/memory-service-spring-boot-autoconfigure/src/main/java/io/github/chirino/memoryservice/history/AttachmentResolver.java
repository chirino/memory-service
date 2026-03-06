package io.github.chirino.memoryservice.history;

import io.github.chirino.memoryservice.client.MemoryServiceClientProperties;
import io.github.chirino.memoryservice.security.SecurityHelper;
import java.io.IOException;
import java.net.URI;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.Paths;
import java.util.ArrayList;
import java.util.Base64;
import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.ai.content.Media;
import org.springframework.core.io.buffer.DataBuffer;
import org.springframework.core.io.buffer.DataBufferUtils;
import org.springframework.http.HttpHeaders;
import org.springframework.http.HttpStatus;
import org.springframework.lang.Nullable;
import org.springframework.security.oauth2.client.OAuth2AuthorizedClientService;
import org.springframework.util.MimeType;
import org.springframework.util.MimeTypeUtils;
import org.springframework.util.StringUtils;
import org.springframework.web.reactive.function.client.WebClient;
import reactor.core.publisher.Flux;
import reactor.core.publisher.Mono;

/**
 * Resolves {@link AttachmentRef} references into {@link Attachments} by downloading each attachment
 * from the memory-service and converting it to the appropriate Spring AI {@link Media} type for LLM
 * delivery. Downloads are streamed to a temp file to avoid buffering large attachments in memory.
 */
public class AttachmentResolver {

    private static final Logger LOG = LoggerFactory.getLogger(AttachmentResolver.class);

    private final MemoryServiceClientProperties properties;
    private final WebClient webClient;
    private final OAuth2AuthorizedClientService authorizedClientService;
    private final Path tempDir;

    public AttachmentResolver(
            MemoryServiceClientProperties properties,
            WebClient.Builder webClientBuilder,
            @Nullable OAuth2AuthorizedClientService authorizedClientService) {
        this.properties = properties;
        this.webClient = webClientBuilder.build();
        this.authorizedClientService = authorizedClientService;
        this.tempDir = resolveTempDir(properties.getTempDir());
    }

    /**
     * Resolves a list of attachment references into an {@link Attachments} object containing both
     * metadata and Media objects.
     */
    public Attachments resolve(List<AttachmentRef> refs) {
        if (refs == null || refs.isEmpty()) {
            return Attachments.empty();
        }

        List<Map<String, Object>> metadata = new ArrayList<>();
        List<Media> media = new ArrayList<>();

        for (AttachmentRef ref : refs) {
            if (!StringUtils.hasText(ref.id()) && !StringUtils.hasText(ref.href())) {
                continue;
            }

            // Build metadata for history recording
            if (StringUtils.hasText(ref.id())) {
                Map<String, Object> meta = new LinkedHashMap<>();
                meta.put("attachmentId", ref.id());
                if (StringUtils.hasText(ref.contentType())) {
                    meta.put("contentType", ref.contentType());
                }
                if (StringUtils.hasText(ref.name())) {
                    meta.put("name", ref.name());
                }
                metadata.add(meta);
            }

            // Download and convert to Media for LLM delivery
            try {
                Media m = downloadAndConvert(ref);
                if (m != null) {
                    media.add(m);
                }
            } catch (Exception e) {
                LOG.warn("Failed to resolve attachment {}, skipping", ref.id(), e);
            }
        }

        return new Attachments(metadata, media);
    }

    @Nullable
    private Media downloadAndConvert(AttachmentRef ref) {
        // If an explicit href is provided (full URL), use it directly
        if (StringUtils.hasText(ref.href())) {
            MimeType mimeType =
                    StringUtils.hasText(ref.contentType())
                            ? MimeTypeUtils.parseMimeType(ref.contentType())
                            : MimeTypeUtils.APPLICATION_OCTET_STREAM;
            return new Media(mimeType, URI.create(ref.href()));
        }

        // Download from memory service
        String baseUrl = properties.getBaseUrl();
        String url = baseUrl + "/v1/attachments/" + ref.id();
        String bearer = SecurityHelper.bearerToken(authorizedClientService);

        var requestSpec = webClient.get().uri(url);
        if (StringUtils.hasText(bearer)) {
            requestSpec = requestSpec.header(HttpHeaders.AUTHORIZATION, "Bearer " + bearer);
        }
        if (StringUtils.hasText(properties.getApiKey())) {
            requestSpec = requestSpec.header("X-API-Key", properties.getApiKey());
        }

        return requestSpec
                .exchangeToMono(
                        response -> {
                            HttpStatus status = HttpStatus.resolve(response.statusCode().value());

                            if (status == HttpStatus.FOUND) {
                                // S3 redirect — use the signed URL directly
                                String signedUrl =
                                        response.headers().header(HttpHeaders.LOCATION).stream()
                                                .findFirst()
                                                .orElse(null);
                                return response.releaseBody()
                                        .thenReturn(toMediaFromUrl(ref.contentType(), signedUrl));
                            }

                            if (status == HttpStatus.OK) {
                                String contentType =
                                        response.headers().header(HttpHeaders.CONTENT_TYPE).stream()
                                                .findFirst()
                                                .orElse(ref.contentType());
                                return streamToTempFileAndConvert(
                                        response.bodyToFlux(DataBuffer.class), contentType);
                            }

                            LOG.warn(
                                    "Unexpected status {} downloading attachment {}",
                                    response.statusCode().value(),
                                    ref.id());
                            return response.releaseBody().then(Mono.empty());
                        })
                .block();
    }

    /**
     * Streams response data buffers to a temp file, then base64-encodes from the file. This avoids
     * buffering the entire attachment in WebClient memory.
     */
    private Mono<Media> streamToTempFileAndConvert(
            Flux<DataBuffer> body, @Nullable String contentType) {
        try {
            Path tempFile = Files.createTempFile(tempDir, "attachment-", ".tmp");
            return DataBufferUtils.write(body, tempFile)
                    .then(
                            Mono.fromCallable(
                                    () -> {
                                        try {
                                            byte[] bytes = Files.readAllBytes(tempFile);
                                            String base64 =
                                                    Base64.getEncoder().encodeToString(bytes);
                                            return toMediaFromBase64(contentType, base64);
                                        } finally {
                                            Files.deleteIfExists(tempFile);
                                        }
                                    }));
        } catch (IOException e) {
            return Mono.error(e);
        }
    }

    private static Path resolveTempDir(@Nullable String configured) {
        if (StringUtils.hasText(configured)) {
            return Paths.get(configured);
        }
        String sysTmp = System.getProperty("java.io.tmpdir");
        return Paths.get(sysTmp);
    }

    @Nullable
    static Media toMediaFromUrl(@Nullable String contentType, @Nullable String url) {
        if (url == null) {
            return null;
        }
        MimeType mimeType =
                StringUtils.hasText(contentType)
                        ? MimeTypeUtils.parseMimeType(contentType)
                        : MimeTypeUtils.IMAGE_PNG;
        return new Media(mimeType, URI.create(url));
    }

    @Nullable
    static Media toMediaFromBase64(@Nullable String contentType, String base64) {
        if (contentType == null) {
            contentType = "application/octet-stream";
        }
        MimeType mimeType = MimeTypeUtils.parseMimeType(contentType);
        String dataUri = "data:" + contentType + ";base64," + base64;
        return new Media(mimeType, URI.create(dataUri));
    }
}
