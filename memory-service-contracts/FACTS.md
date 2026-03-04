# memory-service-contracts facts

- In `CreateEntryRequest` (`openapi.yml` and `memory/v1/memory_service.proto`), `forkedAtEntryId`/`forked_at_entry_id` is optional even when `forkedAtConversationId`/`forked_at_conversation_id` is set.
- When `forkedAtEntryId` is unset during fork-on-append, inherited source entries are excluded; newly appended messages become the first entries of the fork.
