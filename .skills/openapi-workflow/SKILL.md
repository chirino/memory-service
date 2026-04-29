---
name: openapi-workflow
description: Use when making changes to the OpenAPI contract. Workflow for updating spec and regenerating clients.
---

# OpenAPI Change Workflow

1. **Edit the spec**: Agent API changes use `contracts/openapi/openapi.yml`; Admin API changes use `contracts/openapi/openapi-admin.yml`.

2. **Regenerate Go bindings**:
   ```bash
   # Agent API + client
   go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen --config=internal/generated/api/cfg.yaml contracts/openapi/openapi.yml
   go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen --config=internal/generated/apiclient/cfg.yaml contracts/openapi/openapi.yml

   # Admin API
   go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen --config=internal/generated/admin/cfg.yaml contracts/openapi/openapi-admin.yml
   ```

3. **Regenerate Java client**:
   ```bash
   ./java/mvnw -f java/pom.xml -pl quarkus/memory-service-rest-quarkus clean compile -am
   ```

4. **Regenerate TypeScript client**:
   ```bash
   cd frontends/chat-frontend && npm run generate
   ```

5. **Verify**:
   ```bash
   ./java/mvnw -f java/pom.xml compile
   cd frontends/chat-frontend && npm run lint && npm run build
   ```
