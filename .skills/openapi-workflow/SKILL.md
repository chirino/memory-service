---
name: openapi-workflow
description: Use when making changes to the OpenAPI contract. Workflow for updating spec and regenerating clients.
---

# OpenAPI Change Workflow

1. **Edit the spec**: `memory-service-contracts/src/main/resources/openapi.yml`

2. **Regenerate Java client**:
   ```bash
   ./mvnw -pl quarkus/memory-service-rest-quarkus clean compile
   ```

3. **Regenerate TypeScript client**:
   ```bash
   cd common/chat-frontend && npm run generate
   ```

4. **Verify**:
   ```bash
   ./mvnw compile
   cd common/chat-frontend && npm run lint && npm run build
   ```
