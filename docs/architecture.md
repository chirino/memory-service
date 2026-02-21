# Architecture

Memory Service is a stateless backend service that stores and indexes AI agent conversations. It exposes both REST and gRPC APIs, supports pluggable datastores and caching, and is designed for horizontal scaling.

## System Context

The following diagram shows how Memory Service fits into a typical deployment. Agent applications sit between end users and the memory service, mediating all interactions.

```mermaid
C4Context
    title System Context

    Person(user, "End User", "Interacts via chat UI")
    System(agent, "Agent Application", "AI agent (Quarkus / Spring Boot)")
    System(memory, "Memory Service", "Conversation storage, search, access control")
    System_Ext(llm, "LLM Provider", "OpenAI, Ollama, etc.")
    SystemDb(db, "Datastore", "PostgreSQL or MongoDB")
    SystemDb(cache, "Cache", "Redis or Infinispan")

    Rel(user, agent, "Chat messages")
    Rel(agent, llm, "Inference requests")
    Rel(agent, memory, "REST / gRPC")
    Rel(memory, db, "Read / write")
    Rel(memory, cache, "Epoch cache")

    UpdateLayoutConfig($c4ShapeInRow="3", $c4BoundaryInRow="1")
```

## Component Overview

Inside the memory-service, requests flow through API layers into a shared service core that delegates to pluggable store implementations.

```mermaid
block-beta
    columns 3

    block:api["API Layer"]:3
        REST["REST (JAX-RS)"]
        gRPC["gRPC"]
    end

    block:security["Security"]:3
        OIDC["OIDC Auth"]
        APIKey["API Key Auth"]
        RBAC["Role-Based Access"]
    end

    block:core["Service Core"]:3
        Conv["Conversations"]
        Entries["Entries"]
        Search["Search &amp; Indexing"]
        Attach["Attachments"]
        Resumer["Response Resumption"]
        Admin["Admin &amp; Eviction"]
    end

    block:stores["Pluggable Stores"]:3
        MemStore["MemoryStore"]
        VecStore["VectorSearchStore"]
        AttStore["AttachmentStore / FileStore"]
        CacheStore["MemoryEntriesCache"]
    end

    block:impl["Implementations"]:3
        PG["PostgreSQL"]
        Mongo["MongoDB"]
        PGV["pgvector / Qdrant"]
        Redis["Redis"]
        Infinispan["Infinispan"]
        S3["S3"]
    end

    api --> security
    security --> core
    core --> stores
    stores --> impl
```

## Store Abstraction

Memory Service uses interface-based abstractions so that datastores, caches, and file storage can be swapped via configuration. Each interface has two or more implementations selected at startup.

```mermaid
classDiagram
    class MemoryStore {
        <<interface>>
        +createConversation()
        +getEntries()
        +appendMemoryEntries()
        +listConversations()
        ...()
    }
    class PostgresMemoryStore
    class MongoMemoryStore
    class MeteredMemoryStore {
        -delegate : MemoryStore
        +Prometheus metrics
    }
    MemoryStore <|.. PostgresMemoryStore
    MemoryStore <|.. MongoMemoryStore
    MemoryStore <|.. MeteredMemoryStore
    MeteredMemoryStore o-- MemoryStore : wraps

    class VectorSearchStore {
        <<interface>>
        +search()
        +adminSearch()
        +upsertTranscriptEmbedding()
        +deleteByConversationGroupId()
    }
    class PgSearchStore
    class LangChain4jSearchStore
    VectorSearchStore <|.. PgSearchStore
    VectorSearchStore <|.. LangChain4jSearchStore

    class MemoryEntriesCache {
        <<interface>>
        +get()
        +put()
        +evict()
    }
    class RedisMemoryEntriesCache
    class InfinispanMemoryEntriesCache
    class NoopMemoryEntriesCache
    MemoryEntriesCache <|.. RedisMemoryEntriesCache
    MemoryEntriesCache <|.. InfinispanMemoryEntriesCache
    MemoryEntriesCache <|.. NoopMemoryEntriesCache

    class FileStore {
        <<interface>>
        +store()
        +retrieve()
        +delete()
        +getSignedUrl()
    }
    class DatabaseFileStore
    class S3FileStore
    FileStore <|.. DatabaseFileStore
    FileStore <|.. S3FileStore
```

## Request Flow

A typical agent interaction — appending a conversation entry — flows through these layers:

```mermaid
sequenceDiagram
    participant Agent as Agent App
    participant API as REST / gRPC
    participant Auth as Auth Filter
    participant Svc as Service Core
    participant Store as MemoryStore
    participant Cache as EntriesCache
    participant DB as Database

    Agent->>API: POST /v1/conversations/{id}/entries
    API->>Auth: Validate token / API key
    Auth->>Svc: Authorized request
    Svc->>Store: appendMemoryEntries()
    Store->>DB: INSERT entry
    DB-->>Store: OK
    Store-->>Svc: Entry created
    Svc->>Cache: evict(conversationId, clientId)
    Cache-->>Svc: OK
    Svc-->>API: 200 Entry
    API-->>Agent: Response
```

## Search & Indexing Pipeline

Entries can be indexed asynchronously. The agent (or a background job) calls the indexing endpoint, which extracts text, generates embeddings, and stores them for later search.

```mermaid
sequenceDiagram
    participant Agent as Agent App
    participant API as REST / gRPC
    participant Svc as Service Core
    participant Embed as EmbeddingService
    participant Vec as VectorSearchStore
    participant FTS as Full-Text Index
    participant Tasks as Task Queue

    Agent->>API: POST /v1/conversations/index
    API->>Svc: Index entries
    Svc->>Svc: Extract indexedContent from entries
    Svc->>Embed: Generate embeddings (all-MiniLM-L6-v2)
    Embed-->>Svc: 384-dim vectors

    alt Success
        Svc->>Vec: upsertTranscriptEmbedding()
        Svc->>FTS: Update indexed_content / tsvector
        Svc->>Svc: Set indexedAt timestamp
    else Failure
        Svc->>Tasks: Create retry task
    end

    Svc-->>API: 200 OK
    API-->>Agent: Response

    Note over Agent,API: Later — search
    Agent->>API: GET /v1/conversations/search?q=...
    API->>Svc: Search(query)

    alt Semantic search
        Svc->>Embed: Embed query
        Embed-->>Svc: Query vector
        Svc->>Vec: Nearest-neighbor search
        Vec-->>Svc: Matching entries
    else Full-text search
        Svc->>FTS: ts_rank query
        FTS-->>Svc: Matching entries
    end

    Svc-->>API: Search results
    API-->>Agent: Response
```

## Conversation Forking

When a conversation is forked, the new conversation shares the same conversation group and inherits all access-control memberships. Entries before the fork point are shared; new entries diverge.

```mermaid
flowchart LR
    subgraph Group["Conversation Group"]
        direction TB
        M["Memberships<br/>(shared)"]

        subgraph Original["Original Conversation"]
            A["Entry A"] --> B["Entry B<br/>(fork point)"] --> C["Entry C"]
        end

        subgraph Fork["Forked Conversation"]
            B2["Modified Entry B"] --> D["Entry D"] 
        end
    end

    B2 -.->|forked at| B
```

## Access Control Model

Access control is enforced at the conversation group level, so all conversations in a fork tree share the same permissions.

```mermaid
classDiagram
    class AccessLevel {
        <<enumeration>>
        OWNER
        MANAGER
        WRITER
        READER
    }

    class ConversationGroup {
        +UUID id
    }

    class Membership {
        +UUID conversationGroupId
        +String userId
        +AccessLevel accessLevel
    }

    class Conversation {
        +UUID id
        +String ownerUserId
    }

    ConversationGroup "1" --> "*" Membership : grants
    ConversationGroup "1" --> "1..*" Conversation : contains
    Membership --> AccessLevel : level

    note for AccessLevel "OWNER: full control, transfer ownership\nMANAGER: manage memberships\nWRITER: append entries\nREADER: read only"
```

## Attachment Lifecycle

Attachments are uploaded independently, then linked to entries. Unlinked attachments expire automatically.

```mermaid
stateDiagram-v2
    [*] --> Uploaded : POST /attachments
    Uploaded --> Linked : Referenced in entry content
    Uploaded --> Expired : TTL exceeded (default 1h)
    Linked --> SoftDeleted : Entry or conversation deleted
    Expired --> Cleaned : AttachmentCleanupJob
    SoftDeleted --> HardDeleted : Eviction job
    Cleaned --> [*]
    HardDeleted --> [*]

    note right of Linked
        Content-addressed by SHA-256.
        Multiple attachments may share
        the same blob in FileStore.
    end note
```

## Module Structure

The project is organized into a core service module plus framework-specific integration modules for Quarkus and Spring Boot.

```mermaid
flowchart BT
    contracts["memory-service-contracts<br/><small>OpenAPI + Proto specs</small>"]

    restQ["rest-quarkus<br/><small>generated REST client</small>"]
    protoQ["proto-quarkus<br/><small>generated gRPC stubs</small>"]
    restS["rest-spring<br/><small>generated REST client</small>"]
    protoS["proto-spring<br/><small>generated gRPC stubs</small>"]

    encryption["data-encryption<br/><small>DEK / Vault</small>"]
    extension["quarkus extension<br/><small>Dev Services</small>"]

    core["memory-service<br/><small>API implementation</small>"]

    starter["spring-boot-starter<br/><small>compose integration</small>"]
    chatQ["chat-quarkus<br/><small>demo agent</small>"]
    chatS["chat-spring<br/><small>demo agent</small>"]
    frontend["chat-frontend<br/><small>React SPA</small>"]

    restQ --> contracts
    protoQ --> contracts
    restS --> contracts
    protoS --> contracts

    core --> restQ
    core --> protoQ
    core --> encryption

    extension --> restQ
    extension --> protoQ
    chatQ --> extension

    starter --> restS
    starter --> protoS
    chatS --> starter
    frontend --> chatQ 
    frontend --> chatS 
```