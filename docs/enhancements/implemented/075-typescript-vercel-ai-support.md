---
status: proposed
---

# Enhancement 075: TypeScript Vercel AI SDK Support

> **Status**: Proposed.

## Summary

Add first-class TypeScript support for Memory Service using the Vercel AI SDK with Express, including a progressive tutorial + doc-checkpoint flow under a new docs sidebar group: `TypeScript - Vecel AI`.

The goal is to follow the Quarkus tutorial and implementation patterns first (source of truth), while using idiomatic TypeScript patterns that Vercel AI SDK users already follow.

## Motivation

Today, official guided integrations center on Java and Python tracks. TypeScript developers using Vercel AI SDK + Express are a major audience for agent backends and need:

1. A minimal, idiomatic Express + AI SDK starting point.
2. Incremental adoption of Memory Service features (memory, history, search, forking, resumption, sharing).
3. Runnable docs checkpoints with site tests so examples stay correct.

Research baseline: Vercel AI SDK Express cookbook patterns use an Express `POST /chat` route with `streamText(...)`, `convertToModelMessages(...)`, and streamed UI-message responses. This should be our tutorial foundation instead of inventing a custom server pattern.

Implementation baseline: when there is any ambiguity in behavior, endpoint shape, or tutorial sequencing, follow the Quarkus track behavior and tests before consulting Python examples.

## Design

### Scope

Primary scope for this enhancement:

1. TypeScript + Express + Vercel AI SDK integration track.
2. New TypeScript docs section and tutorial pages.
3. New TypeScript checkpoint applications for docs tests.

Out of scope for this enhancement:

1. JavaScript runtime variant implementation (planned follow-up).
2. Non-Vercel TypeScript frameworks (LangChain TS, NestJS, etc.).

### Directory Layout

Create the TypeScript/Vercel AI layout:

```text
typescript/
├── vercelai/
└── examples/
    └── vecelai/
        ├── chat-vecelai-ts/
        └── doc-checkpoints/
```

`typescript/vercelai` is a standalone npm package intended for publication as `@chirino/memory-service-vercelai`. It should contain reusable integration primitives that tutorial examples consume instead of duplicating integration code.

Planned future expansions (not implemented in this enhancement):

```text
javascript/
├── vercelai/
└── examples/
    └── vecelai/
        ├── chat-vecelai-js/
        └── doc-checkpoints/

typescript/
└── examples/
    └── langchain/
        ├── chat-langchain-ts/
        └── doc-checkpoints/
```

### Tutorial / Checkpoint Progression

Implement progressive checkpoints under `typescript/examples/vecelai/doc-checkpoints/`:

1. `01-basic-chat`: plain Express + Vercel AI SDK streaming chat.
2. `02-with-memory`: persist AI state with Memory Service conversation-backed memory channel.
3. `03-with-history`: record USER/AI turns to HISTORY and expose conversation/entry listing endpoints.
4. `04-conversation-forking`: fork via first append using `forkedAtConversationId` + `forkedAtEntryId`.
5. `05a-streaming`: switch to streaming chat (no recording/resume yet).
6. `05b-response-resumption`: add recorder/replay/check/cancel flow.
6. `06-sharing`: add memberships + ownership transfer endpoints.
7. `07-indexing-and-search`: expose `/v1/conversations/search` with supported `searchType` values.

The track is page-driven, not strictly linear checkpoint-to-checkpoint. Follow Quarkus branching patterns where multiple pages start from a stable baseline checkpoint (especially `03-with-history`) instead of always copying the immediately previous numbered checkpoint.

Checkpoint behavior should align with Quarkus examples for:

1. Agent API route semantics and payload shapes.
2. Forking behavior (implicit fork on first append).
3. Search endpoint contract and pagination behavior.
4. Response recording/resumption flow and naming.

### Implementation Phases (Per Tutorial Page)

Each tutorial page is its own implementation/review phase so docs and code can be reviewed and accepted before moving on.

1. Phase 1 - Getting Started page
   - Implement `01-basic-chat` and `02-with-memory`.
   - Publish `getting-started.mdx` with scenarios for both checkpoints.
2. Phase 2 - Conversation History page
   - Implement `03-with-history` from `02-with-memory`.
   - Publish `conversation-history.mdx`.
3. Phase 3 - Conversation Forking page
   - Implement `04-conversation-forking` starting from `03-with-history`.
   - Publish `conversation-forking.mdx`.
4. Phase 4 - Response Recording and Resumption page
   - Implement `05a-streaming` starting from `03-with-history`, then implement `05b-response-resumption` from `05a-streaming`.
   - Publish `response-resumption.mdx`.
5. Phase 5 - Sharing page
   - Implement `06-sharing` starting from `03-with-history` (Quarkus-aligned branch point).
   - Publish `sharing.mdx`.
6. Phase 6 - Indexing and Search page
   - Implement `07-indexing-and-search` starting from `03-with-history` (Quarkus-aligned branch point).
   - Publish `indexing-and-search.mdx`.
7. Phase 7 - Dev Setup + Index pages
   - Publish `dev-setup.mdx` and `index.mdx`.
   - Validate sidebar order and cross-links for the full track.

For each phase, require:

1. Matching checkpoint implementation.
2. Matching page updates.
3. Passing `<TestScenario>` / `<CurlTest>` coverage for that page before starting the next phase.

Phase exit gates:

1. Phase 1 exit gate:
   - `01-basic-chat` and `02-with-memory` build and run.
   - `getting-started.mdx` scenarios pass for both checkpoints.
2. Phase 2 exit gate:
   - `03-with-history` build and run.
   - `conversation-history.mdx` scenarios pass.
3. Phase 3 exit gate:
   - `04-conversation-forking` build and run.
   - `conversation-forking.mdx` scenarios pass.
4. Phase 4 exit gate:
   - `05-response-resumption` build and run.
   - `response-resumption.mdx` scenarios pass.
5. Phase 5 exit gate:
   - `06-sharing` build and run.
   - `sharing.mdx` scenarios pass.
6. Phase 6 exit gate:
   - `07-indexing-and-search` build and run.
   - `indexing-and-search.mdx` scenarios pass.
7. Phase 7 exit gate:
   - Sidebar/nav links resolve for all `typescript-vecelai` pages.
   - `dev-setup.mdx` and `index.mdx` render and link to all guide pages.
   - Full `task test:site` passes with TypeScript scenarios enabled.

### Package / Runtime Conventions

For each TypeScript checkpoint app:

1. Use ESM TypeScript with a standard `src/` layout.
2. Use `npm` as the canonical package manager for docs, examples, and verification commands.
3. Include scripts for `dev`, `build`, and `start`.
4. Keep provider selection and API keys environment-driven.
5. Keep transport defaults aligned with existing Memory Service examples.

For `typescript/vercelai`:

1. Structure as a publishable npm package with versioned `package.json`, build output, and typed exports.
2. Keep public imports stable so examples can use the same package name before and after publication.

### Package Consumption Strategy (Pre-publish and Post-publish)

Examples must import the package using its final package name (`@chirino/memory-service-vercelai`) so no application code changes are needed when the package is published.

Before the package is published, docs should provide a local install workaround:

1. Build/package the local module from `typescript/vercelai`.
2. Install it into each example using npm local dependency mechanisms (`file:` dependency, workspace link, or local tarball via `npm pack`).

After publication, users switch only the dependency source to the published version (for example `npm install @chirino/memory-service-vercelai`) and examples should run without code modifications.

### Docs IA and Sidebar

Add a new sidebar group:

1. Label: `TypeScript - Vecel AI`.
2. Base route: `/docs/typescript-vecelai/`.
3. Child pages:
   - Getting Started
   - Conversation History
   - Indexing and Search
   - Conversation Forking
   - Response Recording and Resumption
   - Sharing
   - Dev Setup

As with existing docs, each page must include `<TestScenario>` and `<CurlTest>` coverage tied to the matching checkpoint.

### Site-test Fit

Follow existing `site/FACTS.md` guidance:

1. Checkpoints are independent runnable apps.
2. Use unique conversation UUIDs across all curl scenarios.
3. Add fixture sets for each checkpoint under the framework-specific path.
4. Regenerate site test scenarios after MDX updates.

Fixture mapping for this track:

1. Checkpoint path prefix: `typescript/examples/vecelai/doc-checkpoints/...`
2. Framework fixture directory: `internal/sitebdd/testdata/openai-mock/fixtures/typescript-vecelai/`
3. Checkpoint fixture directory name: use the checkpoint folder name (for example `03-with-history`).
4. Fixture file pattern: `internal/sitebdd/testdata/openai-mock/fixtures/typescript-vecelai/<checkpoint-name>/001.json`

## Testing

### Unit and App-level

For `typescript/vercelai` and checkpoint apps:

1. Type-check and build each changed package.
2. Validate core request handlers (chat route, memory-service route wrappers).

### Docs / Integration

1. Add `<TestScenario>` + `<CurlTest>` in each new MDX page.
2. Add/update OpenAI mock fixtures for each checkpoint.
3. Run site docs tests to validate end-to-end behavior.

## Tasks

- [ ] Create `typescript/vercelai` as a publishable npm package with stable public exports.
- [ ] Create `typescript/examples/vecelai/chat-vecelai-ts` full example app that imports the package by final npm name.
- [ ] Create `typescript/examples/vecelai/doc-checkpoints/01`, `02`, `03`, `04`, `05a`, `05b`, `06`, and `07` checkpoint apps that import the package by final npm name.
- [ ] Add docs pages under `site/src/pages/docs/typescript-vecelai/`.
- [ ] Add sidebar group `TypeScript - Vecel AI` in `site/src/components/DocsSidebar.astro`.
- [ ] Add site test scenarios + fixtures for new checkpoints.
- [ ] Ensure search docs/examples use `/v1/conversations/search` contract.
- [ ] Add dev setup docs for TypeScript toolchain and env configuration.
- [ ] Document pre-publish local install workflow and post-publish no-code-change workflow.

## Files to Modify

| File | Purpose |
|------|---------|
| `docs/enhancements/075-typescript-vercel-ai-support.md` | This enhancement plan |
| `site/src/components/DocsSidebar.astro` | Add `TypeScript - Vecel AI` navigation section |
| `site/src/pages/docs/typescript-vecelai/index.mdx` | Track landing page |
| `site/src/pages/docs/typescript-vecelai/getting-started.mdx` | `01` + `02` tutorial |
| `site/src/pages/docs/typescript-vecelai/conversation-history.mdx` | `03` tutorial |
| `site/src/pages/docs/typescript-vecelai/indexing-and-search.mdx` | `07` tutorial |
| `site/src/pages/docs/typescript-vecelai/conversation-forking.mdx` | `04` tutorial |
| `site/src/pages/docs/typescript-vecelai/response-resumption.mdx` | `05` tutorial |
| `site/src/pages/docs/typescript-vecelai/sharing.mdx` | `06` tutorial |
| `site/src/pages/docs/typescript-vecelai/dev-setup.mdx` | TypeScript setup and workflow |
| `typescript/vercelai/*` | Publishable npm package for shared TypeScript Vercel AI integration code |
| `typescript/examples/vecelai/chat-vecelai-ts/*` | Full TypeScript example app |
| `typescript/examples/vecelai/doc-checkpoints/*` | Runnable docs checkpoints |
| `internal/sitebdd/**` and/or `site-tests/**` | Site-test support for TypeScript checkpoints + fixtures |

## Verification

```bash
# validate changed TypeScript packages/examples
wt up
wt exec -- bash -lc 'cd /workspace/typescript/vercelai && npm install && npm run build && npm pack'
wt exec -- bash -lc 'cd /workspace/typescript/examples/vecelai/chat-vecelai-ts && npm install && npm run build'

# run docs/site integration checks
wt exec -- bash -lc 'cd /workspace && task test:site > site-test.log 2>&1'
```

## Non-Goals

1. Implement JavaScript variant directories in this enhancement.
2. Implement TypeScript LangChain track in this enhancement.
3. Introduce backward-compatibility shims for pre-release structures.

## Design Decisions

1. Start with Express because it is the canonical Vercel AI SDK API server example and lowest-friction path for TypeScript users.
2. Use Quarkus examples/tests as the primary behavioral reference; use Python tutorial shape as secondary guidance.
3. Keep checkpoint-driven docs parity with existing framework tracks so documentation remains executable.
4. Keep naming and API behavior aligned with current Memory Service contracts (fork behavior, search endpoint, response recording terminology).
5. Standardize on `npm` for the initial TypeScript track to reduce setup variance in docs and site tests.
6. Require examples to consume `typescript/vercelai` via `@chirino/memory-service-vercelai` so publishing the package does not require code changes in examples.

## Open Questions

1. Should response-resumption for TypeScript ship in this same enhancement or be split if gRPC client ergonomics become a blocker?
