package example.agent;

import io.github.chirino.memoryservice.history.ConversationHistoryStreamAdvisor;
import org.springframework.ai.chat.client.ChatClient;
import org.springframework.ai.chat.client.advisor.MessageChatMemoryAdvisor;
import org.springframework.ai.chat.memory.ChatMemory;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;

@Configuration
class AgentChatConfiguration {

    @Bean
    ChatClient agentChatClient(
            ChatClient.Builder chatClientBuilder,
            ChatMemory chatMemory,
            ConversationHistoryStreamAdvisor conversationHistoryStreamAdvisor) {
        MessageChatMemoryAdvisor memoryAdvisor =
                MessageChatMemoryAdvisor.builder(chatMemory)
                        .conversationId(ChatMemory.DEFAULT_CONVERSATION_ID)
                        .build();

        return chatClientBuilder
                .defaultSystem("You are a helpful assistant.")
                .defaultAdvisors(conversationHistoryStreamAdvisor, memoryAdvisor)
                .build();
    }
}
