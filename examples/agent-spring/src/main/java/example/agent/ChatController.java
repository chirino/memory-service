package example.agent;

import org.springframework.ai.chat.client.ChatClient;
import org.springframework.web.bind.annotation.GetMapping;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RequestMapping;
import org.springframework.web.bind.annotation.RequestParam;
import org.springframework.web.bind.annotation.RestController;

@RestController
@RequestMapping("/api/chat")
public class ChatController {

    private final ChatClient chatClient;

    public ChatController(ChatClient.Builder chatClientBuilder) {
        this.chatClient = chatClientBuilder.defaultSystem("You are a helpful assistant.").build();
    }

    @PostMapping
    public ChatResponse chat(@RequestBody ChatRequest request) {
        String reply = chatClient.prompt().user(request.message()).call().content();
        return new ChatResponse(reply);
    }

    @GetMapping
    public ChatResponse chat(@RequestParam("m") String message) {
        String reply = chatClient.prompt().user(message).call().content();
        return new ChatResponse(reply);
    }

    public record ChatRequest(String message) {}

    public record ChatResponse(String reply) {}
}
