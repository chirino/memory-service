# Memory Service

A memory service for AI agents that stores messages exchanged with LLMs and users, supporting conversation replay and forking.

## Recording knowledge

Record something only if it caused a real failure or wasted significant time, and a future session couldn't learn it from the code, tests, contracts (`contracts/`), or site docs. Most discoveries don't qualify. When one does, use the first destination that fits:

1. **Test**: an invariant a test can enforce, including site doc tests for what the docs must say.
2. **Code comment** at the code the gotcha is about. Keep it short.
3. **Skill** (`.skills/<name>/SKILL.md`): a multi-step procedure. Its `description` must name the files or tasks that should trigger it.
4. **This file**: only a cross-cutting trap that most sessions need and that has no code location. One or two lines.

Don't copy contract details (endpoints, enum values, field semantics) into prose; point to the contract. Don't record what a file contains, what changed, or future plans. If you find a record here, in a skill, or in a comment that's wrong, fix or delete it in the same change. If a skill loaded but wasn't relevant, tighten its description.

## Key concepts

- Agent apps sit between end users and the memory service and mediate all operations. The Agent API serves them, and some of its operations are safe to expose to frontends such as SPAs. The Admin API is for administration.
- `contracts/` holds the OpenAPI (`contracts/openapi/`) and protobuf (`contracts/protobuf/`) sources of truth.
- Local developer tooling (`compose.yaml`, `task dev*`, embedded MCP) favors zero-configuration ease of use over production hardening. Keep demo credentials and don't add mandatory secret-generation steps.

## Commands

- `task dev:memory-service` runs the Go service with Air hot reload on port 8082 and starts its dependencies with Docker Compose.
- The Maven wrapper and reactor root are under `java/`. From the repo root, run `./java/mvnw -f java/pom.xml ...`.
- Verify each change with the checks for the modules you touched. The `build-test` skill lists them.

## Rules

- **API parity**: when you change an agent REST operation, update the agent gRPC, admin REST, and admin gRPC surfaces where the operation makes sense, or document why a surface differs. Follow the `api-contract-change` skill.
- **Store transaction scope**: call `MemoryStore` and `EpisodicStore` methods inside `InReadTx`/`InWriteTx` (in Gin handlers, through `internal/plugin/route/routetx`). SQLite panics on unscoped calls.
- **Data compatibility**: changes to persisted structures need an explicit migration path, with tests for upgrade behavior where practical. The core schema baseline is version 2: stores initialize empty datastores and refuse other versions with a reset instruction ([Enhancement 114](docs/enhancements/implemented/114-clean-break-schema-and-compatibility-reset.md)). Don't introduce another reset without explicit approval.
- **Commits**: Conventional Commits (`feat:`, `fix:`, `docs:`). Don't commit secrets; pass them with environment variables.
- **Compatibility markers**: mark every backward-compatibility-only code section with the exact searchable comment `// BACKWARD COMPATIBILITY: remove in a future breaking release.`. Use `# BACKWARD COMPATIBILITY: remove in a future breaking release.` in Dockerfiles and shell files. Add a nearby feature-specific comment when the marker alone doesn't explain the shim.
- **Enhancement docs**: proposed enhancements live in `docs/enhancements/`; others move to `docs/enhancements/<status>/` (`implemented`, `partial`, or `superseded`). Update the doc as each phase lands, and make it match what was built. See the `enhancement-docs` skill.
- **Workarounds**: if you work around a bug, limitation, or missing feature in a dependency:
  1. Record it in `./WORKAROUNDS.md`: what the workaround is, why it was needed, and what a proper fix might look like.
  2. Tell the user, so they can decide whether to accept it or pursue a better fix.
