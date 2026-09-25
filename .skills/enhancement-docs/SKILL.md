---
name: enhancement-docs
description: Use when writing or editing enhancement documents in docs/enhancements/. Provides format, conventions, and numbering guidance.
autoTrigger:
  - files: ["docs/enhancements/*.md", "docs/enhancements/**/*.md"]
---

# Enhancement Document Format

Enhancement docs use the naming convention `NNN-kebab-case-title.md` where NNN is zero-padded and sequential.

- New/proposed docs live in `docs/enhancements/`.
- Non-proposed docs move to `docs/enhancements/<status>/` where status is `implemented`, `partial`, or `superseded`.

Check the highest existing number before creating a new one (sort on file names, not paths, so the status subdirectories don't skew the order):

```bash
find docs/enhancements -name '[0-9]*.md' -exec basename {} \; | sort -n | tail -5
```

Some numbers are shared by two docs (for example `090`, `091`, and `116`). Refer to those docs by file name, not number alone. The `4xx` numbers are a separate range; pick the next number in the main sequence unless told otherwise.

## Required Structure

Every enhancement doc must include:

### 1. YAML Front Matter

```yaml
---
status: proposed
---
```

Valid statuses: `proposed`, `implemented`, `partial`, `superseded`.

When superseded, add:
```yaml
---
status: superseded
superseded-by:
  - implemented/NNN-replacement-name.md
---
```

### 2. Title & Status Line

```markdown
# Enhancement NNN: Brief Descriptive Title

> **Status**: Proposed.
```

For implemented docs: `> **Status**: Implemented.`
For partial: `> **Status**: Partial — see [NNN](../implemented/NNN-name.md) for continuation.`

### 3. Core Sections

| Section | Required | Purpose |
|---------|----------|---------|
| **Summary** | Yes | 1-2 sentence overview of the change |
| **Motivation** | Yes | Why this is needed — current problems, risks, use cases |
| **Design** | Yes | Technical approach — API changes, data model changes, code examples |
| **Testing** | Yes | Test strategy — Cucumber scenarios (gherkin), unit test patterns |
| **Tasks** | Yes | Checkbox list of work items: `- [ ] task` / `- [x] done` |
| **Files to Modify** | Yes | Markdown table of files and what changes in each |
| **Verification** | Yes | Exact commands to compile and run tests |
| **Non-Goals** | Optional | Explicitly out of scope items |
| **Design Decisions** | Optional | Rationale for key choices |
| **Security Considerations** | Optional | Risk mitigations if relevant |
| **Open Questions** | Optional | Unresolved items needing discussion |

### 4. Verification Section (Always Include)

List the exact commands for the modules the change touches (see the `build-test` skill). For example:

```markdown
## Verification

\```bash
go build ./...
CGO_ENABLED=1 go test -race -tags='sqlite_fts5 auth_testfixtures' ./internal/bdd -run '^TestFeaturesSQLite$' -count=1 > test.log 2>&1
./java/mvnw -f java/pom.xml -pl <module> -am compile
\```
```

## Conventions

- **Code blocks**: Use language tags — `java`, `yaml`, `json`, `gherkin`, `bash`, `protobuf`, `sql`
- **Before/after examples**: Show the change clearly with labeled code blocks
- **Tables**: Use markdown tables for field constraints, API parameters, file lists
- **Cross-references**: Link to other enhancements with paths relative to the current doc, for example `[065](implemented/065-go-port.md)` from a proposed doc or `[017](../implemented/017-hide-conversation-groups.md)` from a partial doc
- **Task checkboxes**: Use `- [ ]` for incomplete and `- [x]` for completed tasks
- **Data compatibility stance**: plans that change persisted structures must describe the migration path, compatibility risks, and upgrade validation (see the data compatibility rule in `AGENTS.md`).
- **OpenAPI specs**: Agent API is in `contracts/openapi/openapi.yml`, Admin API is in `contracts/openapi/openapi-admin.yml`
- **Proto file**: gRPC definitions in `contracts/protobuf/memory/v1/memory_service.proto`

## Tips

- Keep the Motivation section concrete — cite specific files, field names, or behaviors that are problematic.
- Include Cucumber test scenarios in gherkin syntax so they can be directly adapted into `.feature` files.
- The Files to Modify table helps scope the work — list every file that needs changes.
- Update the doc as each phase lands. When the implementation diverges from the design, change the doc to describe what was built.
- The Files to Modify table should name the code, tests, docs, and skills that change. Don't plan updates to agent notes; record knowledge the way `AGENTS.md` describes.
