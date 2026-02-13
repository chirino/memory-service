package io.github.chirino.memoryservice.history;

import com.fasterxml.jackson.databind.ObjectMapper;
import io.github.chirino.memoryservice.client.MemoryServiceClientProperties;
import io.github.chirino.memoryservice.grpc.MemoryServiceGrpcClients;
import io.github.chirino.memoryservice.spring.autoconfigure.MemoryServiceAutoConfiguration;
import io.grpc.ManagedChannel;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.ai.chat.client.advisor.api.StreamAdvisor;
import org.springframework.beans.factory.ObjectProvider;
import org.springframework.boot.autoconfigure.AutoConfigureAfter;
import org.springframework.boot.autoconfigure.condition.ConditionalOnBean;
import org.springframework.boot.autoconfigure.condition.ConditionalOnClass;
import org.springframework.boot.autoconfigure.condition.ConditionalOnMissingBean;
import org.springframework.context.annotation.Bean;
import org.springframework.context.annotation.Configuration;
import org.springframework.security.oauth2.client.OAuth2AuthorizedClientService;
import org.springframework.security.oauth2.client.ReactiveOAuth2AuthorizedClientManager;
import org.springframework.web.reactive.function.client.WebClient;

@Configuration(proxyBeanMethods = false)
@ConditionalOnClass(StreamAdvisor.class)
@AutoConfigureAfter(MemoryServiceAutoConfiguration.class)
public class ConversationHistoryAutoConfiguration {

    private static final Logger LOG =
            LoggerFactory.getLogger(ConversationHistoryAutoConfiguration.class);

    @Bean
    public ConversationsApiFactory conversationsApiFactory(
            MemoryServiceClientProperties properties,
            ObjectProvider<WebClient.Builder> webClientBuilderProvider,
            ObjectProvider<ReactiveOAuth2AuthorizedClientManager> managerProvider) {
        WebClient.Builder builder = webClientBuilderProvider.getIfAvailable(WebClient::builder);
        if (builder == null) {
            builder = WebClient.builder();
        }
        return new ConversationsApiFactory(properties, builder, managerProvider.getIfAvailable());
    }

    @Bean
    public ConversationStore conversationStore(
            ConversationsApiFactory conversationsApiFactory,
            ObjectProvider<OAuth2AuthorizedClientService> authorizedClientServiceProvider,
            ObjectProvider<IndexedContentProvider> indexedContentProviderProvider) {
        return new ConversationStore(
                conversationsApiFactory,
                authorizedClientServiceProvider.getIfAvailable(),
                indexedContentProviderProvider.getIfAvailable());
    }

    @Bean
    public ConversationHistoryStreamAdvisorBuilder conversationHistoryStreamAdvisorBuilder(
            ConversationStore conversationStore, ResponseResumer responseResumer) {
        return new ConversationHistoryStreamAdvisorBuilder(conversationStore, responseResumer);
    }

    @Bean
    @ConditionalOnBean({MemoryServiceGrpcClients.MemoryServiceStubs.class, ManagedChannel.class})
    public ResponseResumer grpcResponseResumer(
            MemoryServiceClientProperties clientProperties,
            MemoryServiceGrpcClients.MemoryServiceStubs stubs,
            ManagedChannel channel,
            ObjectProvider<OAuth2AuthorizedClientService> authorizedClientServiceProvider,
            ObjectMapper objectMapper) {
        return new GrpcResponseResumer(
                stubs,
                channel,
                clientProperties,
                authorizedClientServiceProvider.getIfAvailable(),
                objectMapper);
    }

    @Bean
    @ConditionalOnMissingBean(ResponseResumer.class)
    public ResponseResumer noopResponseResumer() {
        return ResponseResumer.noop();
    }

    @Bean
    @ConditionalOnMissingBean
    public AttachmentResolver attachmentResolver(
            MemoryServiceClientProperties properties,
            ObjectProvider<WebClient.Builder> webClientBuilderProvider,
            ObjectProvider<OAuth2AuthorizedClientService> authorizedClientServiceProvider) {
        WebClient.Builder builder = webClientBuilderProvider.getIfAvailable(WebClient::builder);
        return new AttachmentResolver(
                properties, builder, authorizedClientServiceProvider.getIfAvailable());
    }

    @Bean
    @ConditionalOnMissingBean
    public ToolAttachmentExtractor toolAttachmentExtractor() {
        return new DefaultToolAttachmentExtractor();
    }
}
