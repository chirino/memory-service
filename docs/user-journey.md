## Review Feedback

### Structural Issues

1. **No document title or introduction** — Starts with a bare Cacoo diagram link (line 3). Needs a `# User Journey` heading and a brief intro explaining the purpose.

2. **Plain-text formatting instead of markdown** — Uses `•` bullets, no headers (`##`, `###`), no fenced code blocks for JSON/API examples. The JSON on lines 63-65 and 81 is inline text, not formatted code blocks.

---

### Incorrect API Paths & Field Names

3. **Wrong fork endpoint (line 59)** — Uses:
   ```
   POST /v1/conversations/{conversationId}/messages/{messageId}/fork
   ```
   Correct path is:
   ```
   POST /v1/conversations/{conversationId}/entries/{entryId}/fork
   ```
   The resource is called "entries", not "messages."

4. **Wrong field name `forkedAtMessageId` (lines 69, 81)** — Should be `forkedAtEntryId` throughout. Interestingly, line 110 (Scenario 5) uses the correct field name `forkedAtEntryId`, so this is inconsistent within the document itself.

5. **Fabricated `resume-check` endpoint (line 38)** — `POST /v1/conversations/resume-check` does not exist. The resume check is available via the gRPC `ResponseResumerService.CheckConversations` method. The only REST endpoint for response resumption is `DELETE /v1/conversations/{conversationId}/response` (to cancel a stream).

6. **Fabricated `GET .../resume` endpoint (line 45)** — No such REST endpoint. Replay is done via gRPC `ResponseResumerService.ReplayResponseTokens`.

7. **Fabricated Java proxy API (lines 90-95)** — `proxy.forkConversationAtMessage(...)` doesn't exist. The agent would call the same REST or gRPC fork endpoint. This should show the actual API call.

---

### Terminology & Concept Accuracy

8. **"compaction (Epochs)" (line 23)** — The service doesn't have a "compaction" feature. It has **memory epochs** that advance when an agent syncs divergent content via `POST /v1/conversations/{conversationId}/entries/sync`. The word "compaction" implies automatic background processing, but epoch advancement is agent-driven.

9. **"Entry in the history channel" (line 23)** — Correct terminology (entries, history channel), but should be consistent throughout. Earlier lines use "messages" loosely.

10. **Redis hardcoded as the backend (line 46)** — The response resumer is pluggable: Redis, Infinispan, or disabled. Shouldn't name Redis specifically as "the" backend.

11. **Sarah's role description (line 8)** — References `memory-service.roles.admin.users` which is a server config property, not something meaningful to a user-journey audience. Should say she has the **admin** or **auditor** role via OIDC.

---

### Scenario Accuracy Issues

12. **Scenario 6 — Deletion semantics are misleading (lines 113-128)** — The document correctly states "deleting a conversation deletes all conversations in the same fork tree," but then describes the scenario as if James might delete just a leaf branch. The current API doesn't support deleting individual forks — it's all-or-nothing. The "risk" framing and "nuclear option" language implies this is unusual behavior, but it's the only behavior. The paragraph about "the system might restrict this (depending on implementation specifics)" on line 124 is speculative and wrong.

13. **SSE streaming description (line 29)** — "Server-Sent Events (SSE) via the Multi return type in Quarkus" mixes implementation detail (Mutiny `Multi`) with protocol-level description. The user journey should focus on the user experience (streaming responses), not Quarkus internals.

14. **Scenario 4 "Agent Simulation" (lines 84-98)** — The concept of agents forking for simulation is valid, but the description says the agent "merges it back" — there is no merge operation in the API. Forks are one-way. An agent could fork, evaluate, then discard the fork (by deleting the whole tree, which has the cascade problem), or simply ignore it.

15. **Sarah's scenarios use wrong API (lines 34-47, 104-110)** — Sarah is viewing James's conversation data, but the document uses the regular user API endpoints (e.g., `GET /v1/conversations/{suspicious_conversation_id}`). The regular API only returns conversations where the caller has a membership. To view another user's data, Sarah must use the **admin API**: `GET /v1/admin/conversations/{id}`, `GET /v1/admin/conversations/{id}/entries`, etc. These require the admin or auditor role.

---

### Missing Concepts

15. **No mention of access levels** — The service has four distinct access levels (owner, manager, writer, reader) which are central to who can do what. Sarah's admin scenario should reference these.

16. **No sharing/membership scenario** — The service has a full sharing API (`POST /v1/conversations/{conversationId}/memberships`) with access level grants. This is a natural fit for the Sarah-James story.

17. **No search scenario** — Semantic search (`POST /v1/conversations/search`) is a key feature. A scenario where Sarah searches across conversations to find relevant ones would showcase this.

18. **No ownership transfer scenario** — The two-step ownership transfer flow (propose → accept) is a distinct feature worth illustrating.

---

### Summary of Recommended Changes

| Priority | Item | Action |
|----------|------|--------|
| **High** | Fix fork endpoint path | `messages/{messageId}` → `entries/{entryId}` |
| **High** | Fix field name | `forkedAtMessageId` → `forkedAtEntryId` everywhere |
| **High** | Remove fabricated endpoints | `resume-check`, `GET .../resume`, Java proxy API |
| **High** | Fix deletion semantics | Remove speculation; state clearly it's whole-tree deletion |
| **High** | Use admin API for Sarah's scenarios | Sarah must use `/v1/admin/...` endpoints to view James's data, not the regular user API |
| **Medium** | Add proper markdown formatting | Headers, code blocks, structured sections |
| **Medium** | Fix "compaction" terminology | Replace with "memory epochs" and explain agent-driven sync |
| **Medium** | Remove Quarkus internals | SSE/Multi details don't belong in a user journey |
| **Low** | Add missing scenarios | Search, sharing/memberships, ownership transfer |
| **Low** | Add document title and intro | Explain purpose and personas |
