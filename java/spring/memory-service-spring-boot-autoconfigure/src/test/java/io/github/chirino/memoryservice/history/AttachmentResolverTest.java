package io.github.chirino.memoryservice.history;

import static org.assertj.core.api.Assertions.assertThat;
import static org.assertj.core.api.Assertions.assertThatThrownBy;

import com.fasterxml.jackson.databind.ObjectMapper;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.List;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;
import org.springframework.ai.content.Media;
import org.springframework.core.io.FileSystemResource;

class AttachmentResolverTest {

    @TempDir Path tempDir;

    @Test
    void pathConversionCreatesMediaFromPathBytes() throws Exception {
        Path image = tempDir.resolve("image.png");
        Files.write(image, new byte[] {1, 2, 3});

        Media media =
                AttachmentResolver.toMediaFromResource("image/png", new FileSystemResource(image));

        assertThat(media).isNotNull();
        assertThat(media.getMimeType().toString()).isEqualTo("image/png");
        assertThat(media.getData()).isEqualTo(new byte[] {1, 2, 3});
    }

    @Test
    void attachmentsConvertPathMediaLazily() throws Exception {
        Path image = tempDir.resolve("lazy-image.png");
        Files.write(image, new byte[] {1, 2, 3});
        Attachments attachments =
                Attachments.fromDescriptors(
                        List.of(
                                new AttachmentDescriptor(
                                        null, "image/png", null, null, image, null)));

        Files.delete(image);

        assertThatThrownBy(attachments::media).isInstanceOf(RuntimeException.class);
    }

    @Test
    void descriptorMediaCreatesMediaFromPathBytes() throws Exception {
        Path image = tempDir.resolve("descriptor-image.png");
        Files.write(image, new byte[] {1, 2, 3});
        AttachmentDescriptor descriptor =
                new AttachmentDescriptor(null, "image/png", null, null, image, null);

        Media media = descriptor.media();

        assertThat(media).isNotNull();
        assertThat(media.getMimeType().toString()).isEqualTo("image/png");
        assertThat(media.getData()).isEqualTo(new byte[] {1, 2, 3});
    }

    @Test
    void closeDeletesTemporaryAttachmentFiles() throws Exception {
        Path image = tempDir.resolve("close-image.png");
        Files.write(image, new byte[] {1, 2, 3});
        Attachments attachments =
                Attachments.fromDescriptors(
                        List.of(
                                new AttachmentDescriptor(
                                        null, "image/png", null, null, image, null)));

        attachments.close();

        assertThat(Files.exists(image)).isFalse();
    }

    @Test
    void descriptorSerializationOmitsLazySourceFields() throws Exception {
        Path image = tempDir.resolve("metadata-image.png");
        AttachmentDescriptor descriptor =
                new AttachmentDescriptor(
                        "attachment-1",
                        "image/png",
                        "image.png",
                        "/v1/attachments/attachment-1",
                        image,
                        "https://example.test/signed");

        String json = new ObjectMapper().writeValueAsString(descriptor);

        assertThat(json).contains("attachmentId", "contentType", "name", "href");
        assertThat(json).doesNotContain("filePath", "contentUrl", image.toString());
    }
}
