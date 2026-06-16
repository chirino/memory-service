package io.github.chirino.memoryservice.history;

import io.github.chirino.memoryservice.client.MemoryServiceClientProperties;
import io.github.chirino.memoryservice.client.MemoryServiceClients;
import io.github.chirino.memoryservice.security.SecurityHelper;
import java.io.IOException;
import java.net.URI;
import java.nio.file.Files;
import java.nio.file.Path;
import java.nio.file.Paths;
import java.util.ArrayList;
import java.util.List;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.ai.content.Media;
import org.springframework.core.io.Resource;
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
     * descriptors and lazy Media sources.
     */
    public Attachments resolve(List<AttachmentRef> refs) {
        if (refs == null || refs.isEmpty()) {
            return Attachments.empty();
        }

        List<AttachmentDescriptor> descriptors = new ArrayList<>();

        for (AttachmentRef ref : refs) {
            if (!StringUtils.hasText(ref.id()) && !StringUtils.hasText(ref.href())) {
                continue;
            }

            AttachmentDescriptor descriptor = AttachmentDescriptor.fromRef(ref);

            // Download to a local source for lazy Spring AI Media conversion.
            try {
                descriptor = download(descriptor);
            } catch (Exception e) {
                LOG.warn("Failed to resolve attachment {}, skipping", ref.id(), e);
            }
            descriptors.add(descriptor);
        }

        return Attachments.fromDescriptors(descriptors);
    }

    private AttachmentDescriptor download(AttachmentDescriptor descriptor) {
        // If an explicit href is provided (full URL), use it directly
        if (StringUtils.hasText(descriptor.href())) {
            return descriptor.withContentUrl(descriptor.href());
        }

        // Download from memory service
        String baseUrl = MemoryServiceClients.resolveBaseUrl(properties);
        String url = baseUrl + "/v1/attachments/" + descriptor.attachmentId();
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
                                        .thenReturn(urlSource(descriptor, signedUrl));
                            }

                            if (status == HttpStatus.OK) {
                                String contentType =
                                        response.headers().header(HttpHeaders.CONTENT_TYPE).stream()
                                                .findFirst()
                                                .orElse(descriptor.contentType());
                                return streamToTempFile(
                                        descriptor,
                                        response.bodyToFlux(DataBuffer.class),
                                        contentType);
                            }

                            LOG.warn(
                                    "Unexpected status {} downloading attachment {}",
                                    response.statusCode().value(),
                                    descriptor.attachmentId());
                            return response.releaseBody().thenReturn(descriptor);
                        })
                .block();
    }

    /**
     * Streams response data buffers to a temp file. Spring AI Media conversion is deferred until
     * the returned Attachments object's media() method is called.
     */
    private Mono<AttachmentDescriptor> streamToTempFile(
            AttachmentDescriptor descriptor, Flux<DataBuffer> body, @Nullable String contentType) {
        String resolvedContentType =
                StringUtils.hasText(contentType) ? contentType : "application/octet-stream";
        try {
            Path tempFile = Files.createTempFile(tempDir, "attachment-", ".tmp");
            return DataBufferUtils.write(body, tempFile)
                    .then(
                            Mono.fromCallable(
                                    () -> descriptor.withFilePath(resolvedContentType, tempFile)))
                    .onErrorResume(
                            error ->
                                    Mono.fromCallable(
                                                    () -> {
                                                        Files.deleteIfExists(tempFile);
                                                        return descriptor;
                                                    })
                                            .then(Mono.error(error)));
        } catch (IOException e) {
            return Mono.error(e);
        }
    }

    private AttachmentDescriptor urlSource(AttachmentDescriptor descriptor, @Nullable String url) {
        if (!StringUtils.hasText(url)) {
            return descriptor;
        }
        return descriptor.withContentUrl(url);
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
    static Media toMediaFromResource(@Nullable String contentType, Resource resource) {
        if (contentType == null) {
            contentType = "application/octet-stream";
        }
        MimeType mimeType = MimeTypeUtils.parseMimeType(contentType);
        return new Media(mimeType, resource);
    }
}
