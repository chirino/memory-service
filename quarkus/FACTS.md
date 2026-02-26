# Quarkus Module Facts

**Forking curl gotcha**: Checkpoint `04-conversation-forking` chat routes are `text/plain`; to demo fork creation with curl, create root turns via `/chat/{id}` then append forked entries via Memory Service `/v1/conversations/{forkId}/entries` with `forkedAtConversationId`/`forkedAtEntryId`.
