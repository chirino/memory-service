---
name: openapi-workflow
description: Use when making changes to the OpenAPI contract. Workflow for updating spec and regenerating clients.
---

# OpenAPI Change Workflow

1. **Edit the spec**: `contracts/openapi/openapi.yml`

2. **Regenerate Java client**:
   ```bash
   ./java/mvnw -f java/pom.xml -pl quarkus/memory-service-rest-quarkus clean compile -am
   ```

3. **Regenerate TypeScript client**:
   ```bash
   cd frontends/chat-frontend && npm run generate
   ```

4. **Verify**:
   ```bash
   ./java/mvnw -f java/pom.xml compile
   cd frontends/chat-frontend && npm run lint && npm run build
   ```
