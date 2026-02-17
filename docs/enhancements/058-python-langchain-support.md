---
status: proposed
---

# Enhancement 058: Python LangChain Support

> **Status**: Proposed.

## Summary

Add a Python client SDK and LangChain integration for the memory service, with accompanying user documentation. This enables Python-based AI agent applications to use the memory service for conversation history, memory management, and semantic search — matching the existing Quarkus/LangChain4j and Spring AI integrations.

## Motivation

The memory service currently supports two framework integrations:

| Framework | Language | Memory Interface | Content Type Tag |
|-----------|----------|------------------|------------------|
| Quarkus + LangChain4j | Java | `ChatMemoryStore` | `LC4J` |
| Spring Boot + Spring AI | Java | `ChatMemoryRepository` | `SpringAI` |

Python is the dominant language for AI/ML development. LangChain is the most widely used Python framework for building LLM-powered applications. Without Python support, the memory service is limited to Java ecosystems.

### What Developers Need

1. **REST client**: A typed Python client generated from the OpenAPI spec.
2. **LangChain integration**: A `ChatMessageHistory` implementation that stores messages in the memory service.
3. **Authentication**: Support for both API key and OAuth2/OIDC bearer token authentication.
4. **Documentation**: Getting-started guide and progressive feature tutorials (matching the Spring/Quarkus doc pattern).

## Design

### Module Structure

```
python/
├── memory-service-client/          # Generated REST client
│   ├── pyproject.toml
│   ├── memory_service_client/
│   │   ├── __init__.py
│   │   ├── api/                    # Generated API classes
│   │   ├── models/                 # Generated model classes
│   │   └── configuration.py        # Generated config
│   └── README.md
├── memory-service-langchain/       # LangChain integration
│   ├── pyproject.toml
│   ├── memory_service_langchain/
│   │   ├── __init__.py
│   │   ├── chat_message_history.py # ChatMessageHistory implementation
│   │   └── auth.py                 # Auth helpers
│   └── tests/
│       ├── test_chat_message_history.py
│       └── test_auth.py
└── examples/
    └── chat-langchain/             # Example chat app
        ├── pyproject.toml
        ├── app.py
        └── README.md
```

### REST Client Generation

Use `openapi-generator-cli` with the `python` generator:

```bash
openapi-generator-cli generate \
    -i memory-service-contracts/src/main/resources/openapi.yml \
    -g python \
    -o python/memory-service-client \
    --package-name memory_service_client \
    --additional-properties=library=urllib3,projectName=memory-service-client
```

Alternatively, use `openapi-python-client` which produces more idiomatic Python with `httpx` and Pydantic models:

```bash
openapi-python-client generate \
    --path memory-service-contracts/src/main/resources/openapi.yml \
    --output-path python/memory-service-client
```

The generator choice should be evaluated — `openapi-python-client` produces cleaner code but `openapi-generator` has broader ecosystem support.

### Maven Integration

Add a Maven module to trigger client generation during build, similar to how the Spring and Quarkus clients work:

```xml
<plugin>
    <groupId>org.codehaus.mojo</groupId>
    <artifactId>exec-maven-plugin</artifactId>
    <executions>
        <execution>
            <id>generate-python-client</id>
            <phase>generate-sources</phase>
            <goals><goal>exec</goal></goals>
            <configuration>
                <executable>openapi-generator-cli</executable>
                <arguments>
                    <argument>generate</argument>
                    <argument>-i</argument>
                    <argument>${project.basedir}/../memory-service-contracts/src/main/resources/openapi.yml</argument>
                    <argument>-g</argument>
                    <argument>python</argument>
                    <argument>-o</argument>
                    <argument>${project.basedir}/memory-service-client</argument>
                </arguments>
            </configuration>
        </execution>
    </executions>
</plugin>
```

### LangChain Integration

#### `ChatMessageHistory` Implementation

LangChain uses the `BaseChatMessageHistory` interface for conversation memory. The integration stores messages in the memory service's `MEMORY` channel, using content type `LangChain` to distinguish from Java integrations.

```python
from langchain_core.chat_history import BaseChatMessageHistory
from langchain_core.messages import BaseMessage, messages_from_dict, messages_to_dict


class MemoryServiceChatMessageHistory(BaseChatMessageHistory):
    """Chat message history backed by the Memory Service."""

    def __init__(
        self,
        conversation_id: str,
        *,
        base_url: str = "http://localhost:8082",
        api_key: str | None = None,
        bearer_token: str | None = None,
        client_id: str = "langchain",
    ):
        self.conversation_id = conversation_id
        self.client_id = client_id
        self._client = MemoryServiceClient(
            base_url=base_url,
            api_key=api_key,
            bearer_token=bearer_token,
        )

    @property
    def messages(self) -> list[BaseMessage]:
        """Retrieve messages from memory service."""
        entries = self._client.list_entries(
            conversation_id=self.conversation_id,
            channel="memory",
            client_id=self.client_id,
        )
        if not entries:
            return []

        # Latest entry contains the full message window
        latest = entries[-1]
        return messages_from_dict(latest.content)

    def add_messages(self, messages: list[BaseMessage]) -> None:
        """Sync messages to memory service (full window replacement)."""
        self._client.sync_memory(
            conversation_id=self.conversation_id,
            client_id=self.client_id,
            content_type="LangChain",
            content=messages_to_dict(messages),
        )

    def clear(self) -> None:
        """Clear memory by syncing empty content."""
        self._client.sync_memory(
            conversation_id=self.conversation_id,
            client_id=self.client_id,
            content_type="LangChain",
            content=[],
        )
```

#### Authentication Helper

```python
class MemoryServiceClient:
    """Low-level HTTP client for the Memory Service REST API."""

    def __init__(
        self,
        base_url: str,
        api_key: str | None = None,
        bearer_token: str | None = None,
    ):
        self.base_url = base_url.rstrip("/")
        self._session = httpx.Client(base_url=self.base_url)

        if api_key:
            self._session.headers["Authorization"] = f"Bearer {api_key}"
        elif bearer_token:
            self._session.headers["Authorization"] = f"Bearer {bearer_token}"

    def list_entries(self, conversation_id, channel, client_id):
        resp = self._session.get(
            f"/v1/conversations/{conversation_id}/entries",
            params={"channel": channel, "clientId": client_id},
        )
        resp.raise_for_status()
        return resp.json()["data"]

    def sync_memory(self, conversation_id, client_id, content_type, content):
        resp = self._session.post(
            f"/v1/conversations/{conversation_id}/entries",
            json={
                "channel": "memory",
                "clientId": client_id,
                "contentType": content_type,
                "content": content,
            },
        )
        resp.raise_for_status()
        return resp.json()

    def create_conversation(self, title=None, metadata=None):
        resp = self._session.post(
            "/v1/conversations",
            json={"title": title, "metadata": metadata or {}},
        )
        resp.raise_for_status()
        return resp.json()

    def search(self, query, search_type="auto", limit=20):
        resp = self._session.post(
            "/v1/search",
            json={"query": query, "searchType": search_type, "limit": limit},
        )
        resp.raise_for_status()
        return resp.json()
```

#### Usage with LangChain

```python
from langchain_openai import ChatOpenAI
from langchain_core.runnables.history import RunnableWithMessageHistory
from memory_service_langchain import MemoryServiceChatMessageHistory

llm = ChatOpenAI(model="gpt-4")

def get_session_history(session_id: str):
    return MemoryServiceChatMessageHistory(
        conversation_id=session_id,
        base_url="http://localhost:8082",
        api_key="agent-api-key-1",
    )

chain_with_history = RunnableWithMessageHistory(
    llm,
    get_session_history,
)

response = chain_with_history.invoke(
    "What's the capital of France?",
    config={"configurable": {"session_id": "conv-123"}},
)
```

### Example Chat Application

A minimal FastAPI app demonstrating the integration:

```python
from fastapi import FastAPI
from memory_service_langchain import MemoryServiceChatMessageHistory

app = FastAPI()

@app.post("/chat")
async def chat(conversation_id: str, message: str):
    history = MemoryServiceChatMessageHistory(
        conversation_id=conversation_id,
        base_url="http://localhost:8082",
        api_key="agent-api-key-1",
    )
    # ... invoke LLM with history ...
```

### Documentation Plan

Following the pattern from [Enhancement 012 (Spring Site Docs)](012-spring-site-docs.md):

1. **Getting Started** (`site/src/pages/docs/python/getting-started.mdx`)
   - Install packages, configure connection, first conversation
2. **Conversation History** (`site/src/pages/docs/python/conversation-history.mdx`)
   - Recording history channel for frontend replay
3. **Advanced Features** (`site/src/pages/docs/python/advanced-features.mdx`)
   - Forking, search, attachments
4. **REST Client** (`site/src/pages/docs/python/rest-client.mdx`)
   - Direct API usage without LangChain

## Testing

### Unit Tests

```python
class TestMemoryServiceChatMessageHistory:
    def test_messages_returns_empty_for_new_conversation(self, mock_client):
        mock_client.list_entries.return_value = []
        history = MemoryServiceChatMessageHistory("conv-1", client=mock_client)
        assert history.messages == []

    def test_add_messages_calls_sync(self, mock_client):
        history = MemoryServiceChatMessageHistory("conv-1", client=mock_client)
        history.add_messages([HumanMessage(content="hello")])
        mock_client.sync_memory.assert_called_once()

    def test_clear_syncs_empty_content(self, mock_client):
        history = MemoryServiceChatMessageHistory("conv-1", client=mock_client)
        history.clear()
        mock_client.sync_memory.assert_called_with(
            conversation_id="conv-1",
            client_id="langchain",
            content_type="LangChain",
            content=[],
        )
```

### Integration Tests

The example app should include a `docker-compose.yml` that starts the memory service and runs end-to-end tests:

```yaml
services:
  memory-service:
    image: memory-service:latest
    ports:
      - "8082:8082"
    environment:
      MEMORY_SERVICE_API_KEYS_AGENT: agent-api-key-1
```

```bash
# Run integration tests
pytest python/memory-service-langchain/tests/ -m integration
```

## Files to Create

| File | Purpose |
|------|---------|
| `python/memory-service-client/` | **New**: Generated REST client package |
| `python/memory-service-langchain/pyproject.toml` | **New**: Package metadata and dependencies |
| `python/memory-service-langchain/memory_service_langchain/__init__.py` | **New**: Package exports |
| `python/memory-service-langchain/memory_service_langchain/chat_message_history.py` | **New**: `BaseChatMessageHistory` implementation |
| `python/memory-service-langchain/memory_service_langchain/client.py` | **New**: Convenience REST client |
| `python/memory-service-langchain/memory_service_langchain/auth.py` | **New**: Auth helpers |
| `python/memory-service-langchain/tests/` | **New**: Unit and integration tests |
| `python/examples/chat-langchain/` | **New**: Example FastAPI chat app |
| `site/src/pages/docs/python/getting-started.mdx` | **New**: Getting started guide |
| `site/src/pages/docs/python/conversation-history.mdx` | **New**: History recording guide |
| `site/src/pages/docs/python/advanced-features.mdx` | **New**: Advanced features guide |
| `site/src/pages/docs/python/rest-client.mdx` | **New**: Direct REST client guide |

## Design Decisions

1. **`httpx` over `requests`**: `httpx` supports both sync and async, has better typing, and is the standard for modern Python HTTP clients. The generated client may use `urllib3` if using `openapi-generator`, but the LangChain integration wrapper uses `httpx`.
2. **Content type `LangChain`**: Distinct from `LC4J` (Java LangChain4j) and `SpringAI` because message serialization formats differ. Python LangChain uses `messages_to_dict()` which produces a different schema than Java's `JacksonChatMessageJsonCodec`.
3. **Full window sync**: Matching the Java integrations, `add_messages` syncs the entire message window rather than appending individual messages. This matches the memory service's entry consolidation model (Enhancement 026).
4. **Separate packages**: The REST client and LangChain integration are separate packages so users who don't use LangChain can still use the typed client directly.
5. **No gRPC initially**: The Python integration starts with REST only. gRPC support (for streaming response resumption) can be added later if there is demand.

## Implementation Order

1. Set up `python/` directory structure and `pyproject.toml` files
2. Generate REST client from OpenAPI spec
3. Implement `MemoryServiceChatMessageHistory`
4. Write unit tests with mocked HTTP
5. Create example chat app
6. Write site documentation
7. Add integration test with docker-compose
