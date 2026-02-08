# Checkpoint 01: Basic Agent (No Memory)

This checkpoint represents a simple Quarkus application with LangChain4j OpenAI integration, before adding memory service capabilities.

## What's Included

- Basic Quarkus application
- `Agent` interface with `String chat(String userMessage)` method
- `ChatResource` with `POST /chat` endpoint
- LangChain4j OpenAI integration
- No authentication
- No conversation memory
- No persistence

## Tutorial Reference

This checkpoint corresponds to **Step 1** of the [Quarkus Getting Started](https://github.com/chirino/memory-service/blob/main/site/src/pages/docs/quarkus/getting-started.mdx) guide.

## Running

```bash
export OPENAI_API_KEY=your-api-key
./mvnw quarkus:dev
```

## Testing

```bash
curl -NsSfX POST http://localhost:9090/chat \
  -H "Content-Type: application/json" \
  -d '"Hi, I'\''m Hiram, who are you?"'

curl -NsSfX POST http://localhost:9090/chat \
  -H "Content-Type: application/json" \
  -d '"Who am I?"'
```

**Expected behavior**: The agent won't remember who you are - each request is independent.

## Next Step

Continue to [checkpoint 02](../02-with-memory/) to add memory service integration.
