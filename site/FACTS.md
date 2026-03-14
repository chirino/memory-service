
**Response recording docs naming**: Keep doc slugs/routes as `/response-resumption/` for compatibility, but use "Response Recording and Resumption" in page titles, sidebar labels, and cross-links.
**TypeScript response-resumption deps**: `site/src/pages/docs/typescript-vecelai/response-resumption.mdx` must call out `@grpc/grpc-js` and `@grpc/proto-loader` install requirements for checkpoint `05`, because response recorder helpers are gRPC-backed and those packages are optional peers.
**Search docs contract**: `POST /v1/conversations/search` uses `searchType` values `auto`, `semantic`, `fulltext` (not `keyword`) and also accepts an array of concrete types (for example `["semantic","fulltext"]`), with `limit` applied per requested type.
**CodeFromFile match selection**: `site/src/components/CodeFromFile.astro` selects snippet regions via the `match="..."` prop and rejects default-slot matching; docs should always pass match anchors as props.
**CodeFromFile tracked-file gotcha**: `CodeFromFile` reads files from the checked-out repo during Astro build. Do not point it at ignored names like `.env`; use tracked examples such as `.env.example` or CI site builds will fail even if local builds pass.

**Python LangChain response-resumption scope**: `site/src/pages/docs/python-langchain/response-resumption.mdx` checkpoint `05` now demonstrates Memory Service gRPC-backed response recording/replay/cancel behavior and points to `python/examples/langchain/chat-langchain` for the full app implementation.

**Rego code blocks**: Astro's default Shiki setup in this repo does not include bundled `rego` syntax. Rego fences are highlighted via `site/remark-rego-prism.mjs` (Prism `rego` grammar) wired into `astro.config.mjs`, with token colors scoped in `site/src/styles/global.css` under `pre.language-rego`.
**JSON code blocks**: Astro/Shiki emits JSON fences as inline-colored spans without stable token classes in this repo, so `site/src/layouts/DocsLayout.astro` retokenizes `pre[data-language="json"]` on the client and `site/src/styles/global.css` owns the JSON palette.
**Site build optional-deps gotcha**: If `npm run build` fails with missing `@rollup/rollup-<platform>-<arch>`, run `npm install` in `site/` (host or devcontainer) to restore optional Rollup binaries before retrying.

* The examples in the site docs are meant to guide a user through incremntally adding a feature from the memory-service to thier langgraph project.  Some examples build for other examples.  In these cases copy the source of the working previous example and then make modifications to it like a user would make those modifications due to him following the tutorial guide.   Make sure you add the <CurlTest> components to the pages so that you can tun tests to verify that the docs you are showing the user work like you expect them to work.

## Porting Tutorials to New Frameworks / Languages

When porting a tutorial series (e.g., from `python-langchain` to `python-langgraph`), follow these steps to get the docs and site tests working end-to-end.

### 1. Create the Checkpoint App Directory

Each tutorial step is an independent application under `python/examples/<framework>/doc-checkpoints/<NN>-<name>/`.
- Copy the **nearest completed checkpoint** from the source framework as a starting point, then apply incremental changes as the tutorial describes.
- Do not assume checkpoints are always linear (`NN` from `NN-1`). Quarkus tutorial pages branch from stable baselines: for example, `conversation-forking`, `response-resumption`, and `indexing-and-search` all start from `03-with-history`, while `sharing` starts from `05-response-resumption`.
- Each checkpoint must have `pyproject.toml`, `app.py`, and a lockfile (`uv.lock`).
- Give each `pyproject.toml` a unique `name` (e.g. `langgraph-doc-checkpoint-05-response-resumption`).

### 2. Create the MDX Page

Place the page under `site/src/pages/docs/<framework>/`.
- Set `layout`, `title`, `description` in frontmatter.
- Use `<CodeFromFile file="..." lang="python" lines="N-M" />` to embed code snippets.
  - Verify exact line numbers by reading the actual `app.py` before writing `lines=`.
  - After editing `app.py`, re-verify the line numbers; they shift frequently.
- Wrap test scenarios in `<TestScenario checkpoint="python/examples/langgraph/doc-checkpoints/<NN>-<name>">` and each curl test in `<CurlTest steps={`...`}>`.

### 3. Use Unique Conversation UUIDs

**Every `<CurlTest>` that references a conversation ID must use a UUID that is unique across ALL tutorial pages.** Collisions cause 403 Forbidden errors when tests run concurrently. Generate fresh UUIDs with `uuidgen` or similar.

### 4. Add OpenAI Mock Fixtures

The site test runner uses pre-recorded OpenAI fixtures from:
```
internal/sitebdd/testdata/openai-mock/fixtures/<framework>/<checkpoint-name>/001.json
```
The fixture lookup uses:
- `framework` is derived from the checkpoint path:
  - `python/examples/langchain/...` → `python-langchain`
  - `python/examples/langgraph/...` → `python-langgraph`
  - `java/quarkus/examples/...` → `quarkus`
  - `java/spring/examples/...` → `spring`
- `checkpoint-name` = last path segment (e.g. `05-response-resumption`)

LangChain and LangGraph fixtures are in **separate** directories (`python-langchain/` and `python-langgraph/`), so checkpoints with the same name can have independent fixtures.

**Do not bootstrap new checkpoints by copying old fixture files.** Copied fixtures can carry stale UUIDs and other scenario-specific values that break concurrency isolation.

For new checkpoints, generate fixtures from the actual scenarios:
```bash
SITE_TEST_RECORD=missing OPENAI_API_KEY=sk-... task test:site
```

A simple non-streaming fixture (`Content-Type: application/json`) works for `graph.ainvoke()`. A streaming fixture (`Content-Type: text/event-stream`) is required for `graph.astream()`.

### 5. Update the Sidebar Navigation

Add the new pages to `site/src/components/DocsSidebar.astro` in the correct order.

### 6. Delete Stale `test-scenarios.json`

After changing MDX files, delete `site/dist/test-scenarios.json` to force Astro to regenerate it:
```bash
rm -f site/dist/test-scenarios.json
```

### 7. Run Site Tests

```bash
task test:site
```
Or to run in the devcontainer: `wt exec -- bash -c 'task test:site'`

To record OpenAI fixtures for only missing checkpoints (recommended):
```bash
SITE_TEST_RECORD=missing OPENAI_API_KEY=sk-... task test:site
```

After recording fixtures, review the generated model responses and update
`<CurlTest steps={...}>` expectations in the MDX docs when needed (for example
`contains` assertions or JSON snippets that include model-produced text). Newly
recorded fixtures can change response content, so stale expected values will
cause scenario failures.

To force re-recording all fixtures:
```bash
SITE_TEST_RECORD=all OPENAI_API_KEY=sk-... task test:site
```
