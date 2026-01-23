package example.agent;

import org.springframework.ai.chat.client.ChatClient;
import org.springframework.ai.chat.client.advisor.MessageChatMemoryAdvisor;
import org.springframework.ai.chat.memory.ChatMemory;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;

@Configuration
class AgentChatConfiguration {

    @Bean
    ChatClient agentChatClient(ChatClient.Builder chatClientBuilder, ChatMemory chatMemory) {
        MessageChatMemoryAdvisor memoryAdvisor =
                MessageChatMemoryAdvisor.builder(chatMemory)
                        .conversationId(ChatMemory.DEFAULT_CONVERSATION_ID)
                        .build();

        return chatClientBuilder
                .defaultSystem("You are a helpful assistant.")
                .defaultAdvisors(memoryAdvisor)
                .build();
    }
}
