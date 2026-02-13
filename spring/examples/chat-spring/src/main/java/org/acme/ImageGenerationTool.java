package org.acme;

import java.util.LinkedHashMap;
import java.util.List;
import java.util.Map;
import java.util.concurrent.atomic.AtomicReference;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.ai.image.ImageModel;
import org.springframework.ai.image.ImagePrompt;
import org.springframework.ai.image.ImageResponse;
import org.springframework.ai.tool.annotation.Tool;
import org.springframework.ai.tool.annotation.ToolParam;
import org.springframework.stereotype.Component;

/**
 * Spring AI tool that generates images using an image model (e.g., DALL-E 3) and stores the result
 * as an attachment in the memory-service.
 */
@Component
public class ImageGenerationTool {

    private static final Logger LOG = LoggerFactory.getLogger(ImageGenerationTool.class);

    private final ImageModel imageModel;
    private final AttachmentClient attachmentClient;

    /**
     * Per-request list where created attachments are collected so the history advisor can link them
     * to the AI response entry. Set by the controller before each request.
     */
    private final AtomicReference<List<Map<String, Object>>> toolAttachments =
            new AtomicReference<>();

    public ImageGenerationTool(ImageModel imageModel, AttachmentClient attachmentClient) {
        this.imageModel = imageModel;
        this.attachmentClient = attachmentClient;
    }

    /** Set the per-request attachment collector. Call this on the servlet thread before streaming. */
    public void setToolAttachments(List<Map<String, Object>> attachments) {
        toolAttachments.set(attachments);
    }

    @Tool(
            description =
                    "Generate an image based on a text prompt. The image will be displayed"
                        + " automatically as an attachment. Do not include image links or URLs in"
                        + " your response.")
    public String generateImage(
            @ToolParam(description = "The text prompt describing the image to generate")
                    String prompt) {
        LOG.info("Generating image for prompt: {}", prompt);

        try {
            ImageResponse response = imageModel.call(new ImagePrompt(prompt));

            if (response.getResults().isEmpty()) {
                return "{\"error\": \"Image generation returned no results\"}";
            }

            var result = response.getResults().get(0);
            String imageUrl = result.getOutput().getUrl();

            if (imageUrl == null || imageUrl.isBlank()) {
                return "{\"error\": \"Image generation returned no URL\"}";
            }

            // Create attachment from the generated image URL
            String name = generateFilename(prompt);
            Map<String, Object> attachment =
                    attachmentClient.createFromUrl(imageUrl, "image/png", name);

            if (attachment == null) {
                return "{\"error\": \"Failed to create attachment from generated image\"}";
            }

            String attachmentId = String.valueOf(attachment.get("id"));

            // Report the attachment to the per-request collector so the history advisor
            // can link it to the AI response entry.
            List<Map<String, Object>> collector = toolAttachments.get();
            if (collector != null) {
                Map<String, Object> meta = new LinkedHashMap<>();
                meta.put("attachmentId", attachmentId);
                meta.put("contentType", "image/png");
                meta.put("name", name);
                meta.put("href", "/v1/attachments/" + attachmentId);
                collector.add(meta);
            }

            // Return JSON with attachment info so the LLM knows the image was stored.
            // Intentionally omit href so the LLM doesn't embed image URLs in its response.
            return String.format(
                    "{\"attachmentId\": \"%s\", \"contentType\": \"image/png\", \"name\": \"%s\"}",
                    attachmentId, escapeJson(name));
        } catch (Exception e) {
            LOG.error("Failed to generate image for prompt: {}", prompt, e);
            return "{\"error\": \"Image generation failed: " + escapeJson(e.getMessage()) + "\"}";
        }
    }

    private static String generateFilename(String prompt) {
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
