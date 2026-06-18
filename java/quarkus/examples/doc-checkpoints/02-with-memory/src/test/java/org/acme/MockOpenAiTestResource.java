package org.acme;

import com.sun.net.httpserver.HttpExchange;
import com.sun.net.httpserver.HttpServer;
import io.quarkus.test.common.QuarkusTestResourceLifecycleManager;
import java.io.IOException;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.util.Map;

public class MockOpenAiTestResource implements QuarkusTestResourceLifecycleManager {

    private HttpServer server;

    @Override
    public Map<String, String> start() {
        try {
            server = HttpServer.create(new InetSocketAddress("localhost", 0), 0);
            server.createContext("/v1/chat/completions", this::chatCompletions);
            server.start();
            return Map.of(
                    "quarkus.langchain4j.openai.api-key",
                    "test-openai-key",
                    "quarkus.langchain4j.openai.base-url",
                    "http://localhost:" + server.getAddress().getPort() + "/v1",
                    "quarkus.langchain4j.openai.chat-model.model-name",
                    "test-model");
        } catch (IOException e) {
            throw new IllegalStateException("Unable to start mock OpenAI endpoint", e);
        }
    }

    @Override
    public void stop() {
        if (server != null) {
            server.stop(0);
        }
    }

    private void chatCompletions(HttpExchange exchange) throws IOException {
        byte[] response =
                """
                {
                  "id": "chatcmpl-test",
                  "object": "chat.completion",
                  "created": 0,
                  "model": "test-model",
                  "choices": [
                    {
                      "index": 0,
                      "message": {
                        "role": "assistant",
                        "content": "test-response"
                      },
                      "finish_reason": "stop"
                    }
                  ],
                  "usage": {
                    "prompt_tokens": 1,
                    "completion_tokens": 1,
                    "total_tokens": 2
                  }
                }
                """
                        .getBytes(StandardCharsets.UTF_8);
        exchange.getResponseHeaders().set("Content-Type", "application/json");
        exchange.sendResponseHeaders(200, response.length);
        exchange.getResponseBody().write(response);
        exchange.close();
    }
}
