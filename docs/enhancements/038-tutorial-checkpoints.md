---
status: implemented
---

# Tutorial Checkpoint Directories

> **Status**: Implemented.

**Status**: 📋 Planned

## Overview

This enhancement adds checkpoint directories for the Spring and Quarkus tutorials, providing users with working code snapshots at each major tutorial stage. Users can start from any checkpoint to:
- Jump to the middle of the tutorial without completing all prior steps
- Verify their code matches the expected state
- Recover from mistakes without starting over
- See exactly what the code should look like at each stage

## Goals

1. Create progressive checkpoint directories by following tutorial steps exactly
2. Validate that tutorials work correctly and fix any documentation issues found
3. Provide clear reference implementations for each tutorial stage
4. Reduce friction for users learning to integrate memory-service

## Approach: Build Forward by Following Documentation

Instead of working backward from complete examples, we'll:
1. Start from scratch (checkpoint 01)
2. Follow tutorial steps exactly to build each checkpoint
3. Document any issues found in the tutorials
4. Fix documentation if steps don't work
5. Ensure checkpoints match what users would actually build

This validates that tutorials work while creating reference code.

## Checkpoint Structure

```
spring/examples/doc-checkpoints/
  01-basic-agent/              # After getting-started Step 1 (no memory)
  02-with-memory/              # After getting-started Step 2 (memory added)
  03-with-history/             # After conversation-history (history + APIs)
  04-advanced-features/        # After advanced-features (complete)

quarkus/examples/doc-checkpoints/
  01-basic-agent/              # After getting-started Step 1 (no memory)
  02-with-memory/              # After getting-started Step 2 (memory added)
  03-with-history/             # After conversation-history (history + APIs)
  04-advanced-features/        # After advanced-features (complete)
```

## Checkpoint Feature Mapping

### Checkpoint 01: Basic Agent (No Memory)

**Spring (getting-started.mdx Step 1):**
- Simple Spring Boot app from Spring Initializr
- `ChatController` with `POST /chat` endpoint (no conversationId parameter)
- Spring AI OpenAI integration
- No authentication, no memory service
- Package: `com.example.demo`

**Files:**
- `pom.xml` - Spring Boot + Spring AI OpenAI
- `src/main/java/com/example/demo/ChatController.java`
- `src/main/resources/application.properties` (port 9090, OpenAI config)

**Quarkus (getting-started.mdx Step 1):**
- Simple Quarkus app from `quarkus create example`
- `Agent` interface with `String chat(String userMessage)` (no @MemoryId)
- `ChatResource` with `POST /chat` endpoint (no conversationId parameter)
- LangChain4j OpenAI integration
- No authentication, no memory service
- Package: `org.acme`

**Files:**
- `pom.xml` - Quarkus + LangChain4j OpenAI
- `src/main/java/org/acme/Agent.java`
- `src/main/java/org/acme/ChatResource.java`
- `src/main/resources/application.properties` (port 9090, OpenAI config)

### Checkpoint 02: With Memory

**Spring (getting-started.mdx Step 2):**
- Add `memory-service-spring-boot-starter` dependency
- Add `spring-boot-starter-oauth2-resource-server` dependency
- Create `SecurityConfig.java` for OAuth2
- Update `ChatController` to accept `/{conversationId}` path parameter
- Add `MemoryServiceChatMemoryRepositoryBuilder` and `MessageChatMemoryAdvisor`
- Update `application.properties` with memory-service and OAuth2 config

**Quarkus (getting-started.mdx Step 2):**
- Add `memory-service-extension` dependency
- Update `Agent` interface to accept `@MemoryId String conversationId`
- Update `ChatResource` to accept `/{conversationId}` path parameter
- Add OIDC configuration to `application.properties`
- Add memory-service client configuration

### Checkpoint 03: With History

**Spring (conversation-history.mdx):**
- Add `ConversationHistoryStreamAdvisorBuilder` to `ChatController`
- Create `MemoryServiceProxyController` with methods:
  - `getConversation`
  - `listConversationEntries`
  - `listConversations`
- Still using synchronous responses (not streaming yet)

**Quarkus (conversation-history.mdx):**
- Add `quarkus-rest-jackson` dependency
- Create `HistoryRecordingAgent` wrapper with `@RecordConversation`
- Create `ConversationsResource` with methods:
  - `getConversation`
  - `listConversationEntries`
  - `listConversations`
- Update `ChatResource` to inject `HistoryRecordingAgent` instead of `Agent`
- Still using synchronous String responses (not streaming yet)

### Checkpoint 04: Advanced Features

**Spring (advanced-features.mdx):**
- Add fork endpoints to `MemoryServiceProxyController`:
  - `forkConversationAtEntry`
  - `listConversationForks`
- Change `ChatController` to use streaming (`Flux<String>`, `SseEmitter`)
- Create `ResumeController` with:
  - `check` (POST /resume-check)
  - `resume` (GET /{conversationId}/resume)
  - `cancelResponse` (POST /{conversationId}/cancel)

**Quarkus (advanced-features.mdx):**
- Add fork endpoints to `ConversationsResource`:
  - `forkConversationAtEntry`
  - `listConversationForks`
- Change `Agent` to return `Multi<String>` instead of `String`
- Update `HistoryRecordingAgent` to return `Multi<String>`
- Update `ChatResource` to return `Multi<String>`
- Create `ResumeResource` with:
  - `check` (POST /resume-check)
  - `resume` (GET /{conversationId}/resume)
  - `cancelResponse` (POST /{conversationId}/cancel)

## Implementation Steps

### Phase 1: Create Spring Checkpoints

1. **Build Checkpoint 01 (Basic Agent)**
   - Create directory: `spring/examples/doc-checkpoints/01-basic-agent/`
   - Follow getting-started.mdx Step 1 exactly
   - Verify builds and runs: `./mvnw spring-boot:run`
   - Test with curl commands from tutorial
   - Document any issues found

2. **Build Checkpoint 02 (With Memory)**
   - Copy checkpoint 01 to `spring/examples/doc-checkpoints/02-with-memory/`
   - Follow getting-started.mdx Step 2 exactly
   - Verify builds and runs
   - Test with curl commands (requires Memory Service running)
   - Document any issues

3. **Build Checkpoint 03 (With History)**
   - Copy checkpoint 02 to `spring/examples/doc-checkpoints/03-with-history/`
   - Follow conversation-history.mdx exactly
   - Verify builds and runs
   - Test with curl commands
   - Document any issues

4. **Build Checkpoint 04 (Advanced Features)**
   - Copy checkpoint 03 to `spring/examples/doc-checkpoints/04-advanced-features/`
   - Follow advanced-features.mdx exactly
   - Verify builds and runs
   - Test all features
   - Document any issues

### Phase 2: Create Quarkus Checkpoints

Follow same pattern as Spring for all 4 checkpoints.

### Phase 3: Update Documentation

Update each tutorial page to reference checkpoints at the beginning:

**Format for each tutorial section:**
```markdown
## Starting Checkpoint

If you want to start from this point in the tutorial, you can use the checkpoint code:

**Spring**: `spring/examples/doc-checkpoints/02-with-memory/`
**Quarkus**: `quarkus/examples/doc-checkpoints/02-with-memory/`

To use the checkpoint:
```bash
cd spring/examples/doc-checkpoints/02-with-memory
./mvnw spring-boot:run
```
```

**Files to update:**
- `site/src/pages/docs/spring/getting-started.mdx` - Add checkpoint 01 and 02 references
- `site/src/pages/docs/spring/conversation-history.mdx` - Add checkpoint 02 and 03 references
- `site/src/pages/docs/spring/advanced-features.mdx` - Add checkpoint 03 and 04 references
- `site/src/pages/docs/quarkus/getting-started.mdx` - Add checkpoint 01 and 02 references
- `site/src/pages/docs/quarkus/conversation-history.mdx` - Add checkpoint 02 and 03 references
- `site/src/pages/docs/quarkus/advanced-features.mdx` - Add checkpoint 03 and 04 references

### Phase 4: Fix Documentation Issues

As we build checkpoints by following tutorial steps:
- Track any errors or issues encountered
- Fix documentation where steps don't work as written
- Update code examples if they're incorrect
- Ensure all curl commands work

### Phase 5: Add README Files

Create `README.md` in each checkpoint directory explaining:
- What features are included at this checkpoint
- What tutorial section this corresponds to
- How to run the checkpoint
- What to expect when testing
- Link to the relevant tutorial documentation

Example:
```markdown
# Checkpoint 02: With Memory

This checkpoint represents the state after completing Step 2 of the Getting Started guide.

## What's Included

- Memory Service integration via `memory-service-spring-boot-starter`
- OAuth2 authentication with `SecurityConfig`
- Chat endpoint with conversation memory: `POST /chat/{conversationId}`
- Agent remembers context within same conversation

## Prerequisites

- Memory Service running via Docker (see Getting Started)
- Keycloak running for OAuth2 authentication
- OpenAI API key set in environment

## Running

```bash
export OPENAI_API_KEY=your-api-key
./mvnw spring-boot:run
```

## Testing

```bash
# Get auth token
function get-token() {
  curl -sSfX POST http://localhost:8081/realms/memory-service/protocol/openid-connect/token \
    -H "Content-Type: application/x-www-form-urlencoded" \
    -d "client_id=memory-service-client" \
    -d "client_secret=change-me" \
    -d "grant_type=password" \
    -d "username=bob" \
    -d "password=bob" \
    | jq -r '.access_token'
}

# Test chat
curl -NsSfX POST http://localhost:9090/chat/3579aac5-c86e-4b67-bbea-6ec1a3644942 \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $(get-token)" \
  -d '"Hi, I'\''m Hiram, who are you?"'
```

## Next Steps

Continue to Conversation History to add history recording and conversation APIs.
```

## Verification Strategy

For each checkpoint:

1. **Build Verification**
   ```bash
   cd spring/examples/doc-checkpoints/01-basic-agent
   ./mvnw clean compile
   # Should build successfully
   ```

2. **Run Verification**
   ```bash
   export OPENAI_API_KEY=test-key
   ./mvnw spring-boot:run
   # Should start without errors
   ```

3. **Functional Verification**
   - Execute all curl commands from the corresponding tutorial section
   - Verify expected responses
   - Check logs for errors

4. **Tutorial Walkthrough Verification**
   - Start from checkpoint N
   - Follow tutorial steps to reach checkpoint N+1
   - Verify resulting code matches checkpoint N+1

5. **Documentation Cross-Check**
   - Compare checkpoint code against tutorial code examples
   - Ensure all dependencies match
   - Ensure all configuration matches

## Success Criteria

- ✅ All 8 checkpoints build successfully (4 Spring + 4 Quarkus)
- ✅ All curl commands from tutorials work correctly
- ✅ Users can start from any checkpoint and follow remaining tutorial steps
- ✅ Documentation accurately reflects working code
- ✅ Any found documentation issues are fixed
- ✅ README files provide clear instructions for each checkpoint

## Benefits

1. **Reduced Learning Friction**: Users can jump to any tutorial section without completing all prior steps
2. **Error Recovery**: Users can compare their code against working checkpoints to identify mistakes
3. **Tutorial Validation**: Building checkpoints validates that tutorial steps actually work
4. **Documentation Quality**: Forces us to ensure documentation is accurate and complete
5. **Better Onboarding**: New users can more easily explore different memory-service features

## Maintenance Notes

**When tutorials change:**
1. Update affected checkpoints by following updated steps
2. Verify all subsequent checkpoints still work
3. Update README files if features change
4. Re-test all curl commands

**When memory-service APIs change:**
1. Review impact on each checkpoint
2. Update checkpoints that use affected APIs
3. Update tutorial documentation
4. Re-verify all checkpoints build and run

## Related Enhancements

- [Enhancement 009: Spring Boot Support](./009-springboot-support.md) - Added Spring Boot starter used in checkpoints
- [Enhancement 012: Spring Site Docs](./012-spring-site-docs.md) - Created the tutorial documentation
- [Enhancement 022: Chat App Design](./022-chat-app-design.md) - Frontend referenced in tutorials
- [Enhancement 023: Chat App Implementation](./023-chat-app-implementation.md) - Demo app used for testing
