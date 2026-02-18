---
name: enhancement-docs
description: Use when writing or editing enhancement documents in docs/enhancements/. Provides format, conventions, and numbering guidance.
autoTrigger:
  - files: ["docs/enhancements/*.md"]
---

# Enhancement Document Format

Enhancement docs live in `docs/enhancements/` and follow the naming convention `NNN-kebab-case-title.md` where NNN is zero-padded and sequential. Check the highest existing number before creating a new one:

```bash
ls docs/enhancements/ | sort -n | tail -3
```

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
  - NNN-replacement-name.md
---
```

### 2. Title & Status Line

```markdown
# Enhancement NNN: Brief Descriptive Title

> **Status**: Proposed.
```

For implemented docs: `> **Status**: Implemented.`
For partial: `> **Status**: Partial — see [NNN](NNN-name.md) for continuation.`

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

```markdown
## Verification

\```bash
# Compile
./mvnw compile

# Run tests
./mvnw test -pl memory-service > test.log 2>&1
# Search for failures using Grep tool on test.log
\```
```

## Conventions

- **Code blocks**: Use language tags — `java`, `yaml`, `json`, `gherkin`, `bash`, `protobuf`, `sql`
- **Before/after examples**: Show the change clearly with labeled code blocks
- **Tables**: Use markdown tables for field constraints, API parameters, file lists
- **Cross-references**: Link to other enhancements with `[NNN](NNN-name.md)` relative links
- **Task checkboxes**: Use `- [ ]` for incomplete and `- [x]` for completed tasks
- **Pre-release stance**: No backward compatibility needed. Don't deprecate — just delete/rename. Datastores are reset frequently.
- **OpenAPI specs**: Agent API is in `openapi.yml`, Admin API is in `openapi-admin.yml`, both under `memory-service-contracts/src/main/resources/`
- **Proto file**: gRPC definitions in `memory-service-contracts/src/main/resources/memory/v1/memory_service.proto`

## Tips

- Keep the Motivation section concrete — cite specific files, field names, or behaviors that are problematic.
- Include Cucumber test scenarios in gherkin syntax so they can be directly adapted into `.feature` files.
- The Files to Modify table helps scope the work — list every file that needs changes.
- When the implementation diverges from the design, update the enhancement doc to reflect reality (per CLAUDE.md instructions).
