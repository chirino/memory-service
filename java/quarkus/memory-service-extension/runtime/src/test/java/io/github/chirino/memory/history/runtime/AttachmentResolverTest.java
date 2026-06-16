package io.github.chirino.memory.history.runtime;

import static org.assertj.core.api.Assertions.assertThat;
import static org.assertj.core.api.Assertions.assertThatThrownBy;

import com.fasterxml.jackson.databind.ObjectMapper;
import dev.langchain4j.data.message.Content;
import dev.langchain4j.data.message.ImageContent;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.List;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.io.TempDir;

class AttachmentResolverTest {

    @TempDir Path tempDir;

    @Test
    void pathConversionCreatesInlineImageContent() throws Exception {
        Path image = tempDir.resolve("image.png");
        Files.write(image, new byte[] {1, 2, 3});

        Content content = AttachmentResolver.toContentFromPath("image/png", image);

        assertThat(content).isInstanceOf(ImageContent.class);
        ImageContent imageContent = (ImageContent) content;
        assertThat(imageContent.image().url()).isNull();
        assertThat(imageContent.image().base64Data()).isEqualTo("AQID");
        assertThat(imageContent.image().mimeType()).isEqualTo("image/png");
    }

    @Test
    void attachmentsConvertPathContentLazily() throws Exception {
        Path image = tempDir.resolve("lazy-image.png");
        Files.write(image, new byte[] {1, 2, 3});

        Attachments attachments =
                Attachments.fromDescriptors(
                        List.of(
                                new AttachmentDescriptor(
                                        null, "image/png", null, null, image, null)));

        Files.delete(image);

        assertThatThrownBy(attachments::contents).isInstanceOf(RuntimeException.class);
    }

    @Test
    void descriptorContentCreatesInlineImageContent() throws Exception {
        Path image = tempDir.resolve("descriptor-image.png");
        Files.write(image, new byte[] {1, 2, 3});
        AttachmentDescriptor descriptor =
                new AttachmentDescriptor(null, "image/png", null, null, image, null);

        Content content = descriptor.content();

        assertThat(content).isInstanceOf(ImageContent.class);
        ImageContent imageContent = (ImageContent) content;
        assertThat(imageContent.image().url()).isNull();
        assertThat(imageContent.image().base64Data()).isEqualTo("AQID");
        assertThat(imageContent.image().mimeType()).isEqualTo("image/png");
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
