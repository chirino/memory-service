package org.acme;

import dev.langchain4j.agent.tool.Tool;
import dev.langchain4j.model.image.ImageModel;
import dev.langchain4j.model.output.Response;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import java.net.URI;
import java.util.Map;
import org.jboss.logging.Logger;

/**
 * LangChain4j tool that generates images using an image model (e.g., DALL-E 3) and stores the
 * result as an attachment in the memory-service.
 */
@ApplicationScoped
public class ImageGenerationTool {

    private static final Logger LOG = Logger.getLogger(ImageGenerationTool.class);

    @Inject ImageModel imageModel;

    @Inject AttachmentClient attachmentClient;

    @Tool(
            "Generate an image based on a text prompt. The image will be displayed automatically"
                    + " as an attachment. Do not include image links or URLs in your response.")
    public String generateImage(String prompt) {
        LOG.infof("Generating image for prompt: %s", prompt);

        try {
            Response<dev.langchain4j.data.image.Image> response = imageModel.generate(prompt);
            dev.langchain4j.data.image.Image image = response.content();

            URI imageUrl = image.url();
            if (imageUrl == null) {
                return "{\"error\": \"Image generation returned no URL\"}";
            }

            // Create attachment from the generated image URL
            String name = generateFilename(prompt);
            Map<String, Object> attachment =
                    attachmentClient.createFromUrl(imageUrl.toString(), "image/png", name);

            if (attachment == null) {
                return "{\"error\": \"Failed to create attachment from generated image\"}";
            }

            // Return JSON with attachment info so the recording layer can link it.
            // Intentionally omit href so the LLM doesn't embed image URLs in its response.
            return String.format(
                    "{\"attachmentId\": \"%s\", \"contentType\": \"image/png\", \"name\": \"%s\"}",
                    attachment.get("id"), escapeJson(name));
        } catch (Exception e) {
            LOG.errorf(e, "Failed to generate image for prompt: %s", prompt);
            return "{\"error\": \"Image generation failed: " + escapeJson(e.getMessage()) + "\"}";
        }
    }

    private static String generateFilename(String prompt) {
        // Create a short filename from the prompt
        String clean = prompt.replaceAll("[^a-zA-Z0-9\\s]", "").trim();
        if (clean.length() > 40) {
            clean = clean.substring(0, 40);
        }
        clean = clean.replaceAll("\\s+", "-").toLowerCase();
        if (clean.isEmpty()) {
            clean = "generated-image";
        }
        return clean + ".png";
    }

    private static String escapeJson(String value) {
        if (value == null) {
            return "";
        }
        return value.replace("\\", "\\\\").replace("\"", "\\\"");
    }
}
