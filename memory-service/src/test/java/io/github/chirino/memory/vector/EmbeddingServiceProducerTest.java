package io.github.chirino.memory.vector;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertInstanceOf;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.util.Optional;
import org.junit.jupiter.api.Test;

class EmbeddingServiceProducerTest {

    private EmbeddingServiceProducer createProducer(String type) {
        EmbeddingServiceProducer producer = new EmbeddingServiceProducer();
        producer.embeddingType = type;
        producer.openaiApiKey = Optional.empty();
        producer.genericOpenaiApiKey = Optional.empty();
        producer.openaiModelName = "text-embedding-3-small";
        producer.openaiBaseUrl = "https://api.openai.com/v1";
        producer.openaiDimensions = Optional.empty();
        return producer;
    }

    @Test
    void selects_local_by_default() {
        EmbeddingServiceProducer producer = createProducer("local");
        EmbeddingService service = producer.embeddingService();

        assertInstanceOf(LocalEmbeddingService.class, service);
        assertTrue(service.isEnabled());
        assertEquals(384, service.dimensions());
        assertEquals("local/all-MiniLM-L6-v2", service.modelId());
    }

    @Test
    void selects_disabled_for_none() {
        EmbeddingServiceProducer producer = createProducer("none");
        EmbeddingService service = producer.embeddingService();

        assertInstanceOf(DisabledEmbeddingService.class, service);
        assertFalse(service.isEnabled());
        assertEquals(0, service.dimensions());
        assertEquals("none", service.modelId());
    }

    @Test
    void openai_requires_api_key() {
        EmbeddingServiceProducer producer = createProducer("openai");
        // No API key set

        assertThrows(IllegalStateException.class, producer::embeddingService);
    }

    @Test
    void selects_openai_with_config() {
        EmbeddingServiceProducer producer = createProducer("openai");
        producer.openaiApiKey = Optional.of("sk-test-key");
        producer.openaiModelName = "text-embedding-3-small";
        producer.openaiDimensions = Optional.of(512);

        EmbeddingService service = producer.embeddingService();

        assertInstanceOf(OpenAiEmbeddingService.class, service);
        assertTrue(service.isEnabled());
        assertEquals(512, service.dimensions());
        assertEquals("openai/text-embedding-3-small", service.modelId());
    }

    @Test
    void openai_falls_back_to_generic_api_key() {
        EmbeddingServiceProducer producer = createProducer("openai");
        producer.genericOpenaiApiKey = Optional.of("sk-generic-key");
        producer.openaiDimensions = Optional.of(512);

        EmbeddingService service = producer.embeddingService();

        assertInstanceOf(OpenAiEmbeddingService.class, service);
    }

    @Test
    void openai_specific_key_takes_precedence() {
        EmbeddingServiceProducer producer = createProducer("openai");
        producer.openaiApiKey = Optional.of("sk-specific-key");
        producer.genericOpenaiApiKey = Optional.of("sk-generic-key");
        producer.openaiDimensions = Optional.of(512);

        EmbeddingService service = producer.embeddingService();

        assertInstanceOf(OpenAiEmbeddingService.class, service);
    }

    @Test
    void rejects_unknown_type() {
        EmbeddingServiceProducer producer = createProducer("cohere");

        IllegalStateException ex =
                assertThrows(IllegalStateException.class, producer::embeddingService);
        assertTrue(ex.getMessage().contains("cohere"));
    }
}
