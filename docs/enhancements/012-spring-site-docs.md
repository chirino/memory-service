# Spring Boot / Spring AI Documentation

## Goals

- Provide comprehensive user documentation for Spring Boot and Spring AI integration with Memory Service
- Mirror the structure of the existing Quarkus documentation for consistency
- Guide users from a simple Spring AI chat application to a full-featured agent with persistent memory, conversation history, and advanced features
- Document available starters, autoconfiguration, and service connection support

## Reference Implementation

The `spring/examples/chat-spring` directory contains a complete working example that demonstrates all integration patterns. The documentation will reference this example extensively:

| File | Purpose |
|------|---------|
| `AgentStreamController.java` | Main chat endpoint with streaming, memory, and history recording |
| `MemoryServiceProxyController.java` | Proxy endpoints for conversation APIs (list, get, fork, etc.) |
| `ResumeController.java` | Response resumption check endpoint |
| `MemoryServiceConfig.java` | `MemoryServiceProxy` bean configuration |
| `SecurityConfig.java` | OAuth2 login configuration |
| `application.properties` | Memory Service and OAuth2 configuration |
| `compose.yaml` | Docker Compose setup with all required services |
| `pom.xml` | Maven dependencies including `memory-service-spring-boot-starter` |

## Documentation Structure

The documentation will live at `site/src/pages/docs/spring/` with the following pages:

| File | Title | Description |
|------|-------|-------------|
| `getting-started.mdx` | Spring Getting Started | Basic Spring AI app with ChatMemory backed by Memory Service |
| `conversation-history.mdx` | Conversation History | Recording history for frontend display with ConversationHistoryStreamAdvisor |
| `advanced-features.mdx` | Advanced Features | Streaming, forking, response resumption, and cancellation |
| `rest-client.mdx` | Spring REST Client | Using the generated WebClient-based REST client directly |
| `grpc-client.mdx` | Spring gRPC Client | Using the gRPC client for streaming and high-performance scenarios |
| `docker-compose.mdx` | Docker Compose Integration | Service connection support for development |

## Page Details

### 1. Getting Started (`getting-started.mdx`)

**Goal:** Take users from zero to a working Spring AI agent with persistent chat memory.

**Content outline:**

1. **Prerequisites**
   - Java 21+
   - Maven
   - Docker (for Memory Service)

2. **Build from Source** (same as Quarkus docs - project is in POC phase)

3. **Step 1: Create a Simple Spring AI App**
   - Use Spring Initializr or manual setup
   - Add `spring-ai-starter-model-openai` dependency (as shown in `chat-spring/pom.xml`)
   - Create a simple `ChatClient` based controller:
     ```java
     @RestController
     public class ChatController {
         private final ChatClient chatClient;

         public ChatController(ChatClient.Builder builder) {
             this.chatClient = builder.build();
         }

         @PostMapping("/chat")
         public String chat(@RequestBody String message) {
             return chatClient.prompt().user(message).call().content();
         }
     }
     ```
   - Configure OpenAI credentials in `application.properties` (ref: `chat-spring/src/main/resources/application.properties`):
     ```properties
     spring.ai.openai.api-key=${OPENAI_API_KEY:}
     spring.ai.openai.base-url=${OPENAI_BASE_URL:https://api.openai.com}
     ```
   - Test with curl - note that there's no memory between requests

4. **Step 2: Add Memory Service Starter**
   - Add dependencies (ref: `chat-spring/pom.xml`):
     ```xml
     <dependency>
       <groupId>io.github.chirino.memory-service</groupId>
       <artifactId>memory-service-spring-boot-starter</artifactId>
       <version>${project.version}</version>
     </dependency>
     ```
   - Start Memory Service via Docker Compose (ref: `chat-spring/compose.yaml`)
   - Configure connection in `application.properties`:
     ```properties
     memory-service.client.base-url=${MEMORY_SERVICE_URL:http://localhost:8082}
     memory-service.client.api-key=${MEMORY_SERVICE_API_KEY:}
     memory-service.client.log-requests=true
     ```
   - Configure OAuth2 login (ref: `chat-spring/src/main/resources/application.properties`):
     ```properties
     spring.security.oauth2.client.registration.memory-service-client.client-id=memory-service-client
     spring.security.oauth2.client.registration.memory-service-client.client-secret=change-me
     spring.security.oauth2.client.registration.memory-service-client.scope=openid,profile,email
     spring.security.oauth2.client.registration.memory-service-client.authorization-grant-type=authorization_code
     spring.security.oauth2.client.registration.memory-service-client.redirect-uri={baseUrl}/login/oauth2/code/{registrationId}
     spring.security.oauth2.client.provider.memory-service-client.issuer-uri=http://localhost:8081/realms/memory-service
     ```
   - Configure security (ref: `chat-spring/src/main/java/example/agent/SecurityConfig.java`):
     ```java
     @Configuration
     class SecurityConfig {
         @Bean
         SecurityFilterChain securityFilterChain(HttpSecurity http) throws Exception {
             http.csrf(csrf -> csrf.disable())
                 .authorizeHttpRequests(auth -> auth.anyRequest().authenticated())
                 .oauth2Login(oauth2 -> oauth2.defaultSuccessUrl("/", false))
                 .logout(logout -> logout.logoutSuccessUrl("/"));
             return http.build();
         }
     }
     ```

5. **Step 3: Add Chat Memory**
   - Inject `MemoryServiceChatMemoryRepositoryBuilder` (auto-configured by starter)
   - Build `MessageWindowChatMemory` with the repository
   - Add `MessageChatMemoryAdvisor` to ChatClient (ref: `AgentStreamController.java`):
     ```java
     // Capture bearer token on the HTTP request thread
     String bearerToken = SecurityHelper.bearerToken(authorizedClientService);
     var repository = repositoryBuilder.build(bearerToken);
     var chatMemory = MessageWindowChatMemory.builder()
             .chatMemoryRepository(repository)
             .build();

     var chatClient = chatClientBuilder.clone()
             .defaultSystem("You are a helpful assistant.")
             .defaultAdvisors(MessageChatMemoryAdvisor.builder(chatMemory).build())
             .build();

     return chatClient.prompt()
             .advisors(advisor -> advisor.param(ChatMemory.CONVERSATION_ID, conversationId))
             .user(message)
             .call()
             .content();
     ```
   - Test with curl - agent now remembers context within conversation

6. **Next Steps** - Link to Conversation History and Advanced Features

### 2. Conversation History (`conversation-history.mdx`)

**Goal:** Show how to record conversation history for frontend display.

**Content outline:**

1. **Prerequisites** - Completed Getting Started guide

2. **Understanding Memory vs History**
   - Agent memory (internal context window for LLM)
   - Conversation history (what users see in UI, stored in `history` channel)

3. **Enable History Recording**
   - Inject `ConversationHistoryStreamAdvisorBuilder` (auto-configured by starter)
   - Add the advisor to ChatClient alongside memory advisor (ref: `AgentStreamController.java`):
     ```java
     String bearerToken = SecurityHelper.bearerToken(authorizedClientService);
     var historyAdvisor = historyAdvisorBuilder.build(bearerToken);
     var repository = repositoryBuilder.build(bearerToken);
     var chatMemory = MessageWindowChatMemory.builder().chatMemoryRepository(repository).build();

     var chatClient = chatClientBuilder.clone()
             .defaultSystem("You are a helpful assistant.")
             .defaultAdvisors(
                     historyAdvisor,
                     MessageChatMemoryAdvisor.builder(chatMemory).build())
             .build();
     ```
   - The `historyAdvisor` automatically records user messages and agent responses

4. **Configure MemoryServiceProxy**
   - Create `MemoryServiceProxy` bean (ref: `MemoryServiceConfig.java`):
     ```java
     @Bean
     MemoryServiceProxy memoryServiceProxy(
             MemoryServiceClientProperties properties,
             WebClient.Builder webClientBuilder,
             ObjectProvider<OAuth2AuthorizedClientService> authorizedClientService,
             ObjectProvider<MemoryServiceConnectionDetails> connectionDetailsProvider) {
         // Apply connection details if available (from Docker Compose)
         MemoryServiceConnectionDetails connectionDetails = connectionDetailsProvider.getIfAvailable();
         if (connectionDetails != null) {
             if (!StringUtils.hasText(properties.getApiKey())) {
                 properties.setApiKey(connectionDetails.getApiKey());
             }
             if (!StringUtils.hasText(properties.getBaseUrl())) {
                 properties.setBaseUrl(connectionDetails.getBaseUrl());
             }
         }
         return new MemoryServiceProxy(properties, webClientBuilder, authorizedClientService.getIfAvailable());
     }
     ```

5. **Expose Conversation APIs**
   - Create proxy controller (ref: `MemoryServiceProxyController.java`):
     ```java
     @RestController
     @RequestMapping("/v1/conversations")
     class MemoryServiceProxyController {
         private final MemoryServiceProxy proxy;

         @GetMapping
         public ResponseEntity<?> listConversations(
                 @RequestParam(required = false) String mode,
                 @RequestParam(required = false) String after,
                 @RequestParam(required = false) Integer limit,
                 @RequestParam(required = false) String query) {
             return proxy.listConversations(mode, after, limit, query);
         }

         @GetMapping("/{conversationId}")
         public ResponseEntity<?> getConversation(@PathVariable String conversationId) {
             return proxy.getConversation(conversationId);
         }

         @GetMapping("/{conversationId}/messages")
         public ResponseEntity<?> listConversationMessages(
                 @PathVariable String conversationId,
                 @RequestParam(required = false) String after,
                 @RequestParam(required = false) Integer limit) {
             return proxy.listConversationMessages(conversationId, after, limit);
         }

         @DeleteMapping("/{conversationId}")
         public ResponseEntity<?> deleteConversation(@PathVariable String conversationId) {
             return proxy.deleteConversation(conversationId);
         }
     }
     ```
   - Test with curl to see messages in conversation

6. **Next Steps** - Link to Advanced Features

### 3. Advanced Features (`advanced-features.mdx`)

**Goal:** Cover streaming, forking, and response resumption.

**Content outline:**

1. **Prerequisites** - Completed previous guides

2. **Conversation Forking**
   - Add fork endpoint to proxy (ref: `MemoryServiceProxyController.java`):
     ```java
     @PostMapping("/{conversationId}/messages/{messageId}/fork")
     public ResponseEntity<?> forkConversationAtMessage(
             @PathVariable String conversationId,
             @PathVariable String messageId,
             @RequestBody(required = false) String body) {
         return proxy.forkConversationAtMessage(conversationId, messageId, body);
     }

     @GetMapping("/{conversationId}/forks")
     public ResponseEntity<?> listConversationForks(@PathVariable String conversationId) {
         return proxy.listConversationForks(conversationId);
     }
     ```
   - Test forking from a message with curl

3. **Streaming Responses**
   - Switch from `call()` to `stream()` on ChatClient
   - Use `SseEmitter` for Server-Sent Events (ref: `AgentStreamController.java`):
     ```java
     @PostMapping(path = "/{conversationId}/sse",
             consumes = MediaType.APPLICATION_JSON_VALUE,
             produces = MediaType.TEXT_EVENT_STREAM_VALUE)
     public SseEmitter stream(@PathVariable String conversationId, @RequestBody MessageRequest request) {
         String bearerToken = SecurityHelper.bearerToken(authorizedClientService);
         var historyAdvisor = historyAdvisorBuilder.build(bearerToken);
         var repository = repositoryBuilder.build(bearerToken);
         var chatMemory = MessageWindowChatMemory.builder().chatMemoryRepository(repository).build();

         var chatClient = chatClientBuilder.clone()
                 .defaultSystem("You are a helpful assistant.")
                 .defaultAdvisors(historyAdvisor, MessageChatMemoryAdvisor.builder(chatMemory).build())
                 .build();

         Flux<String> responseFlux = chatClient.prompt()
                 .advisors(advisor -> advisor.param(ChatMemory.CONVERSATION_ID, conversationId))
                 .user(request.getMessage())
                 .stream()
                 .chatClientResponse()
                 .map(this::extractContent);

         SseEmitter emitter = new SseEmitter(0L);
         Disposable subscription = responseFlux.subscribe(
                 chunk -> safeSendChunk(emitter, new TokenFrame(chunk)),
                 emitter::completeWithError,
                 emitter::complete);

         emitter.onCompletion(subscription::dispose);
         emitter.onTimeout(() -> { subscription.dispose(); emitter.complete(); });
         return emitter;
     }
     ```

4. **Response Resumption**
   - Inject `ResponseResumer` (auto-configured when gRPC is available)
   - Implement resume endpoint (ref: `AgentStreamController.java`):
     ```java
     @GetMapping(path = "/{conversationId}/resume", produces = MediaType.TEXT_EVENT_STREAM_VALUE)
     public SseEmitter resume(@PathVariable String conversationId) {
         SseEmitter emitter = new SseEmitter(0L);
         String bearerToken = SecurityHelper.bearerToken(authorizedClientService);

         Disposable subscription = responseResumer.replay(conversationId, bearerToken)
                 .subscribe(
                         chunk -> safeSendChunk(emitter, new TokenFrame(chunk)),
                         emitter::completeWithError,
                         emitter::complete);

         emitter.onCompletion(subscription::dispose);
         return emitter;
     }
     ```
   - Implement check endpoint (ref: `ResumeController.java`):
     ```java
     @PostMapping("/resume-check")
     public List<String> resumeCheck(@RequestBody List<String> conversationIds) {
         return responseResumer.check(conversationIds, SecurityHelper.bearerToken(authorizedClientService));
     }
     ```
   - Implement cancel endpoint via `MemoryServiceProxy.cancelResponse()`

5. **Complete Example** - Reference `spring/examples/chat-spring`

### 4. REST Client (`rest-client.mdx`)

**Goal:** Document direct REST client usage for advanced scenarios.

**Content outline:**

1. **Setup**
   - The starter includes the REST client automatically
   - Inject `ApiClient` bean

2. **API Overview**
   - `ConversationsApi` - CRUD for conversations
   - `UserConversationsApi` - User-scoped operations
   - `SearchApi` - Semantic search

3. **Example Operations**
   - List conversations
   - Create conversation
   - Get/update/delete conversation
   - Add messages
   - Search messages

4. **Configuration Properties**
   - `memory-service.client.base-url`
   - `memory-service.client.api-key`
   - `memory-service.client.bearer-token`
   - `memory-service.client.log-requests`

### 5. gRPC Client (`grpc-client.mdx`)

**Goal:** Document gRPC client for streaming and high-performance scenarios.

**Content outline:**

1. **When to Use gRPC**
   - Response resumption (required)
   - High-throughput scenarios
   - Real-time streaming

2. **Setup**
   - gRPC is auto-configured when `memory-service.grpc.enabled=true`
   - Or auto-derived from REST client base URL

3. **Injecting Stubs**
   - Inject `MemoryServiceGrpcClients.MemoryServiceStubs`
   - Access blocking and async stubs

4. **Example Operations**
   - Streaming message subscription
   - Response resumption internals

5. **Configuration Properties**
   - `memory-service.grpc.enabled`
   - `memory-service.grpc.target`
   - `memory-service.grpc.plaintext`

### 6. Docker Compose Integration (`docker-compose.mdx`)

**Goal:** Document service connection support for development.

**Content outline:**

1. **Overview**
   - Spring Boot 3.1+ Docker Compose support
   - Automatic service discovery and configuration

2. **Setup**
   - Add `spring-boot-docker-compose` dependency (ref: `chat-spring/pom.xml`):
     ```xml
     <dependency>
       <groupId>org.springframework.boot</groupId>
       <artifactId>spring-boot-docker-compose</artifactId>
     </dependency>
     ```
   - Place `compose.yaml` in project root (Spring Boot auto-detects it)

3. **Complete compose.yaml Example** (ref: `chat-spring/compose.yaml`)
   ```yaml
   services:
     postgres:
       image: postgres:16-alpine
       environment:
         POSTGRES_DB: memory_service
         POSTGRES_USER: postgres
         POSTGRES_PASSWORD: postgres
       ports:
         - "55432:5432"
       healthcheck:
         test: ["CMD-SHELL", "pg_isready -U postgres"]
         interval: 5s
         timeout: 5s
         retries: 10

     redis:
       image: redis:7-alpine
       command: ["redis-server", "--save", "", "--appendonly", "no"]
       ports:
         - "56379:6379"
       healthcheck:
         test: ["CMD", "redis-cli", "ping"]
         interval: 5s
         timeout: 3s
         retries: 10

     keycloak:
       image: quay.io/keycloak/keycloak:24.0.5
       command: ["start-dev", "--import-realm"]
       environment:
         KEYCLOAK_ADMIN: admin
         KEYCLOAK_ADMIN_PASSWORD: admin
         KC_DB: postgres
         KC_DB_URL_HOST: postgres
         KC_DB_URL_DATABASE: keycloak
         KC_DB_USERNAME: keycloak
         KC_DB_PASSWORD: keycloak
         KC_HOSTNAME: ${KEYCLOAK_HOSTNAME:-localhost}
         KC_HOSTNAME_PORT: ${KEYCLOAK_HOSTNAME_PORT:-8081}
         KC_HOSTNAME_BACKCHANNEL_DYNAMIC: "true"
       volumes:
         - ./keycloak/memory-service-realm.json:/opt/keycloak/data/import/memory-service-realm.json:ro
       depends_on:
         postgres:
           condition: service_healthy
       ports:
         - "8081:8080"

     memory-service:
       image: ghcr.io/chirino/memory-service:latest
       environment:
         MEMORY_SERVICE_API_KEYS_AGENT: agent-api-key-1,agent-api-key-2
         QUARKUS_DATASOURCE_JDBC_URL: jdbc:postgresql://postgres:5432/memory_service
         QUARKUS_DATASOURCE_USERNAME: postgres
         QUARKUS_DATASOURCE_PASSWORD: postgres
         QUARKUS_OIDC_AUTH_SERVER_URL: http://keycloak:8080/realms/memory-service
         QUARKUS_OIDC_TOKEN_ISSUER: http://localhost:8081/realms/memory-service
         MEMORY_SERVICE_CACHE_TYPE: redis
         QUARKUS_REDIS_HOSTS: redis://redis:6379
       depends_on:
         postgres:
           condition: service_healthy
         redis:
           condition: service_healthy
         keycloak:
           condition: service_started
       ports:
         - "8082:8080"
       healthcheck:
         test: ["CMD-SHELL", "curl -f http://localhost:8080/v1/health || exit 1"]
         interval: 10s
         timeout: 5s
         retries: 12
   ```

4. **Service Connection**
   - Spring Boot auto-detects the memory-service container
   - `MemoryServiceConnectionDetails` provides `baseUrl` and `apiKey` to your app
   - Configuration in `MemoryServiceConfig.java` shows how to use connection details

## Implementation Plan

### Phase 1: Core Documentation
1. Create `site/src/pages/docs/spring/` directory
2. Implement `getting-started.mdx` following the outline above
3. Implement `conversation-history.mdx`
4. Implement `advanced-features.mdx`

### Phase 2: Client Documentation
5. Implement `rest-client.mdx`
6. Implement `grpc-client.mdx`

### Phase 3: Development Experience
7. Implement `docker-compose.mdx`

### Phase 4: Navigation and Polish
8. Update site navigation to include Spring section (parallel to Quarkus)
9. Add cross-links between Spring and Quarkus docs where appropriate
10. Review and test all code examples against `examples/chat-spring`

## Dependencies

- Existing `examples/chat-spring` as reference implementation
- Memory Service running via Docker Compose for testing examples
- Spring Boot 3.x with Spring AI

## Differences from Quarkus Documentation

| Aspect | Quarkus | Spring |
|--------|---------|--------|
| AI Framework | LangChain4j | Spring AI |
| Memory Integration | `@MemoryId` annotation | `ChatMemory.CONVERSATION_ID` advisor param |
| History Recording | `@RecordConversation` interceptor | `ConversationHistoryStreamAdvisor` |
| Streaming | `Multi<String>` (Mutiny) | `Flux<String>` (Reactor) / `SseEmitter` |
| Dev Services | Quarkus Dev Services | Docker Compose service connection |
| DI | CDI (`@Inject`) | Spring (`@Autowired` / constructor) |
| REST Client | REST Client (generated) | WebClient (generated) |
| Security | Quarkus OIDC | Spring Security OAuth2 Client |

## Success Criteria

1. A Spring Boot developer can follow the getting-started guide and have a working memory-backed agent
2. All code examples compile and work with the current `examples/chat-spring` implementation
3. Documentation covers the same feature set as the Quarkus documentation
4. Navigation is intuitive with clear progression from basic to advanced topics
