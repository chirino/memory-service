# Typical Load Patterns

This document records the default load-pattern assumptions for a heavily used memory-service deployment. These assumptions are meant to guide capacity planning, performance work, cache design, and benchmark scenarios. They describe the expected shape of traffic, not hard protocol limits.

## Summary

The dominant production pattern is many users interacting concurrently, with each individual user driving only a small number of active conversations at a time. Per-user request rates are modest. System-wide load becomes high because those modest per-user streams are multiplied across many concurrent users and agent applications.

## Single-User Assumptions

For one end user:

- The user is typically focused on one active conversation.
- While an LLM response is in progress, the user may start one or two additional conversations.
- The user is therefore usually receiving updates from only a few conversations at once.
- Event rates per conversation are low to moderate, not bursty at infrastructure scale.

For one active conversation:

- The agent app appends history entries when the user sends a message and when the assistant completes or streams a response.
- The agent app appends context-channel entries at a regular cadence while the LLM request is active.
- The highest-frequency writes usually come from context updates around tool-call boundaries, intermediate reasoning state, retrieval state, or other agent progress markers.
- These writes are still relatively low rate for a single conversation. The pattern is more like steady trickle traffic than high-frequency market-data-style streaming.

## Concurrent Conversation Assumptions

Even for engaged users, concurrency is limited:

- Most users have 1 active conversation.
- Some users temporarily have 2 or 3 active conversations.
- It is not typical for a single user to fan out across many simultaneous high-rate conversations.

This matters because the service should be optimized for large numbers of users each touching a small working set of conversations, rather than a small number of users each generating extreme per-conversation write rates.

## Fleet-Level Load Shape

Under heavy production load, traffic is the aggregate of many similar user sessions:

- Many users concurrently read and append to their own small set of active conversations.
- Each user contributes a low-rate stream of writes and reads.
- The server experiences high total QPS because of concurrency across users, not because any one user or conversation is especially hot.

This creates a classic fan-in pattern:

- Low to moderate write rate per conversation.
- Low conversation fan-out per user.
- High aggregate write and read throughput across the fleet.
- Large numbers of independent hot conversation keys spread across the datastore and cache layers.

## Expected Request Mix

The typical request mix is expected to look roughly like this:

- Conversation-scoped reads to assemble LLM context.
- Conversation-scoped appends for user, assistant, and context-channel entries.
- Conversation listing or lightweight metadata reads when a user opens the UI or switches threads.
- Occasional search, fork, or replay operations, but not as the dominant steady-state load.

In steady state:

- Append operations are frequent and latency-sensitive.
- Recent-conversation reads are frequent and latency-sensitive.
- Search and broader historical retrieval are important, but usually secondary to the core append/read loop of active conversations.

## Burst Characteristics

Typical bursts are short and localized:

- A user message can trigger a small burst of reads to fetch context, followed by a sequence of context-channel appends while the agent works.
- Tool-heavy agent turns can increase the number of intermediate appends.
- Assistant completion can trigger a final append and follow-up reads from clients refreshing the conversation view.

These bursts are expected to stay bounded per conversation. The main scaling problem is many bounded bursts happening at once across many users.

## Design Implications

These assumptions suggest the service should favor:

- Efficient append performance for small conversation-local writes.
- Efficient reads of the most recent entries for a conversation.
- Cache and storage strategies that work well with many concurrently hot conversation keys.
- Horizontal scaling across many independent conversation streams.
- Backpressure and connection management sized for high concurrency rather than unusually high per-connection throughput.

## Non-Goals of This Model

This model does not assume:

- A single user streaming dozens of active conversations simultaneously.
- Extremely high-frequency append traffic within one conversation.
- Search-heavy workloads dominating normal interactive usage.
- Large analytical scans as the primary production load.

Those scenarios may still matter for stress testing, but they are not the baseline "typical heavy usage" pattern described here.
