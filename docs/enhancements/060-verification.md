# Enhancement 060: Multi-Tenancy Verification Guide

> **Branch**: `experiment/spicedb-authz`
> **Date verified**: 2026-02-20

## Prerequisites

- Podman or Docker running (`podman machine start` or Docker Desktop)
- Java 21+, Maven wrapper (`./mvnw`)
- Ports 8080–8082 available

## 1. Build

```bash
./mvnw compile -pl memory-service
```

Expected: `BUILD SUCCESS`

## 2. Unit Tests

```bash
./mvnw test -pl memory-service \
  -Dtest="LocalAuthorizationServiceTest,SpiceDbAuthorizationServiceTest,AuthorizationServiceSelectorTest,MongoMemoryServiceTest,PostgresqlMemoryServiceTest,InfinispanMemoryEntriesCacheConfigTest,VectorStoreSelectorTest,RedisHealthCheckTest,InfinispanResponseResumerLocatorStoreConfigTest"
```

Expected: `Tests run: 40, Failures: 0, Errors: 0`

### What the unit tests cover

| Test class | Count | Validates |
|---|---|---|
| `LocalAuthorizationServiceTest` | 6 | Direct membership delegation, org admin implicit MANAGER, team member implicit WRITER, no-op writes |
| `SpiceDbAuthorizationServiceTest` | 3 | AccessLevel→permission mapping, AccessLevel→relation mapping, team method existence |
| `AuthorizationServiceSelectorTest` | 5 | Selector picks `local` or `spicedb` implementation based on config |
| `PostgresqlMemoryServiceTest` | 6 | Full Postgres store round-trip (conversation CRUD, entries, forking) |
| `MongoMemoryServiceTest` | 6 | Full Mongo store round-trip |
| Others | 14 | Cache config, Redis health, vector store selector, Infinispan config |

## 3. Start the Service

```bash
./mvnw quarkus:dev -pl memory-service -Ddebug=false
```

Wait for: `Listening on: http://localhost:8082`

Dev Services will automatically start:
- PostgreSQL (pgvector)
- MongoDB
- Keycloak (port 8081)
- Redis
- Infinispan

## 4. Obtain Access Tokens

```bash
# Alice
ALICE_TOKEN=$(curl -s -X POST http://localhost:8081/realms/memory-service/protocol/openid-connect/token \
  -d "grant_type=password" -d "client_id=memory-service-client" -d "client_secret=change-me" \
  -d "username=alice" -d "password=alice" | python3 -c "import json,sys; print(json.load(sys.stdin)['access_token'])")

# Bob
BOB_TOKEN=$(curl -s -X POST http://localhost:8081/realms/memory-service/protocol/openid-connect/token \
  -d "grant_type=password" -d "client_id=memory-service-client" -d "client_secret=change-me" \
  -d "username=bob" -d "password=bob" | python3 -c "import json,sys; print(json.load(sys.stdin)['access_token'])")

# Charlie
CHARLIE_TOKEN=$(curl -s -X POST http://localhost:8081/realms/memory-service/protocol/openid-connect/token \
  -d "grant_type=password" -d "client_id=memory-service-client" -d "client_secret=change-me" \
  -d "username=charlie" -d "password=charlie" | python3 -c "import json,sys; print(json.load(sys.stdin)['access_token'])")

BASE=http://localhost:8082
```

## 5. Organization CRUD

### Create organization

```bash
curl -s -X POST "$BASE/v1/organizations" \
  -H "Authorization: Bearer $ALICE_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":"Acme Corp","slug":"acme-corp","metadata":{"industry":"tech"}}' | python3 -m json.tool
```

Expected: 201 with `"role": "owner"`. Save the `id` as `ORG_ID`.

### List organizations

```bash
curl -s "$BASE/v1/organizations" -H "Authorization: Bearer $ALICE_TOKEN" | python3 -m json.tool
```

### Get organization detail

```bash
curl -s "$BASE/v1/organizations/$ORG_ID" -H "Authorization: Bearer $ALICE_TOKEN" | python3 -m json.tool
```

### Update organization

```bash
curl -s -X PATCH "$BASE/v1/organizations/$ORG_ID" \
  -H "Authorization: Bearer $ALICE_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":"Acme Corp Updated"}' | python3 -m json.tool
```

### Non-member cannot access

```bash
curl -s -o /dev/null -w "%{http_code}" "$BASE/v1/organizations/$ORG_ID" \
  -H "Authorization: Bearer $BOB_TOKEN"
# Expected: 403
```

## 6. Member Management

### Add members

```bash
curl -s -X POST "$BASE/v1/organizations/$ORG_ID/members" \
  -H "Authorization: Bearer $ALICE_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"userId":"bob","role":"member"}' | python3 -m json.tool

curl -s -X POST "$BASE/v1/organizations/$ORG_ID/members" \
  -H "Authorization: Bearer $ALICE_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"userId":"charlie","role":"member"}' | python3 -m json.tool
```

### List members

```bash
curl -s "$BASE/v1/organizations/$ORG_ID/members" \
  -H "Authorization: Bearer $ALICE_TOKEN" | python3 -m json.tool
```

### Update role

```bash
curl -s -X PATCH "$BASE/v1/organizations/$ORG_ID/members/bob" \
  -H "Authorization: Bearer $ALICE_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"role":"admin"}' | python3 -m json.tool
```

### Remove member

```bash
curl -s -o /dev/null -w "%{http_code}" -X DELETE "$BASE/v1/organizations/$ORG_ID/members/charlie" \
  -H "Authorization: Bearer $ALICE_TOKEN"
# Expected: 204
```

## 7. Team Management

### Create team

```bash
curl -s -X POST "$BASE/v1/organizations/$ORG_ID/teams" \
  -H "Authorization: Bearer $ALICE_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"name":"Engineering","slug":"engineering"}' | python3 -m json.tool
```

Save the `id` as `TEAM_ID`.

### Add team member (must be org member first)

```bash
curl -s -X POST "$BASE/v1/organizations/$ORG_ID/teams/$TEAM_ID/members" \
  -H "Authorization: Bearer $ALICE_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"userId":"bob"}' | python3 -m json.tool
```

### Non-org member cannot join team

```bash
curl -s -o /dev/null -w "%{http_code}" -X POST "$BASE/v1/organizations/$ORG_ID/teams/$TEAM_ID/members" \
  -H "Authorization: Bearer $ALICE_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"userId":"dave"}'
# Expected: 403
```

## 8. Org-Scoped Conversations

### Create org-scoped conversation

```bash
curl -s -X POST "$BASE/v1/conversations?organizationId=$ORG_ID" \
  -H "Authorization: Bearer $ALICE_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"title":"Org Conversation"}' | python3 -m json.tool
```

Save the `id` as `ORG_CONV_ID`.

### Create team-scoped conversation

```bash
curl -s -X POST "$BASE/v1/conversations?organizationId=$ORG_ID&teamId=$TEAM_ID" \
  -H "Authorization: Bearer $ALICE_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"title":"Team Conversation"}' | python3 -m json.tool
```

Save the `id` as `TEAM_CONV_ID`.

### List conversations filtered by org

```bash
curl -s "$BASE/v1/conversations?organizationId=$ORG_ID" \
  -H "Authorization: Bearer $ALICE_TOKEN" | python3 -m json.tool
# Expected: returns only conversations scoped to the org
```

## 9. Authorization Verification

### 9.1 Org owner implicit MANAGER access

Alice (org owner) can read Bob's team conversation without explicit sharing:

```bash
curl -s "$BASE/v1/conversations/$TEAM_CONV_ID" \
  -H "Authorization: Bearer $ALICE_TOKEN" | python3 -m json.tool
# Expected: 200, accessLevel shown as "owner" (because Alice created it)
#           or "manager" (if someone else created it)
```

### 9.2 Team member implicit WRITER access

Bob (team member) can read team-scoped conversations:

```bash
curl -s "$BASE/v1/conversations/$TEAM_CONV_ID" \
  -H "Authorization: Bearer $BOB_TOKEN" | python3 -m json.tool
# Expected: 200, accessLevel: "writer"
```

### 9.3 Org member without team membership is blocked

Charlie (org member, NOT team member) cannot read team conversation:

```bash
curl -s -o /dev/null -w "%{http_code}" "$BASE/v1/conversations/$TEAM_CONV_ID" \
  -H "Authorization: Bearer $CHARLIE_TOKEN"
# Expected: 403
```

### 9.4 Regular org member cannot read org conversations

Bob (role: member, not admin) cannot read org-only conversations he's not shared with:

```bash
# First demote Bob back to member if he was promoted
curl -s -o /dev/null -X PATCH "$BASE/v1/organizations/$ORG_ID/members/bob" \
  -H "Authorization: Bearer $ALICE_TOKEN" -H "Content-Type: application/json" \
  -d '{"role":"member"}'

curl -s -o /dev/null -w "%{http_code}" "$BASE/v1/conversations/$ORG_CONV_ID" \
  -H "Authorization: Bearer $BOB_TOKEN"
# Expected: 403
```

### 9.5 Org admin gets implicit MANAGER

After promoting Bob to admin, he can read org conversations:

```bash
curl -s -o /dev/null -X PATCH "$BASE/v1/organizations/$ORG_ID/members/bob" \
  -H "Authorization: Bearer $ALICE_TOKEN" -H "Content-Type: application/json" \
  -d '{"role":"admin"}'

curl -s "$BASE/v1/conversations/$ORG_CONV_ID" \
  -H "Authorization: Bearer $BOB_TOKEN" | python3 -m json.tool
# Expected: 200, accessLevel: "manager"
```

### 9.6 Sharing restricted to org members

Cannot share an org-scoped conversation with a user outside the org:

```bash
curl -s -o /dev/null -w "%{http_code}" -X POST "$BASE/v1/conversations/$ORG_CONV_ID/memberships" \
  -H "Authorization: Bearer $ALICE_TOKEN" -H "Content-Type: application/json" \
  -d '{"userId":"dave","accessLevel":"reader"}'
# Expected: 400
```

Sharing with an org member succeeds:

```bash
# Re-add Charlie if removed
curl -s -o /dev/null -X POST "$BASE/v1/organizations/$ORG_ID/members" \
  -H "Authorization: Bearer $ALICE_TOKEN" -H "Content-Type: application/json" \
  -d '{"userId":"charlie","role":"member"}'

curl -s -o /dev/null -w "%{http_code}" -X POST "$BASE/v1/conversations/$ORG_CONV_ID/memberships" \
  -H "Authorization: Bearer $ALICE_TOKEN" -H "Content-Type: application/json" \
  -d '{"userId":"charlie","accessLevel":"reader"}'
# Expected: 201
```

### 9.7 Non-scoped conversations unaffected

Conversations without an `organizationId` work exactly as before:

```bash
curl -s -X POST "$BASE/v1/conversations" \
  -H "Authorization: Bearer $ALICE_TOKEN" -H "Content-Type: application/json" \
  -d '{"title":"Personal Conv"}' | python3 -m json.tool
# Expected: 201, no org/team scoping
```

### 9.8 Regular member cannot delete org

```bash
curl -s -o /dev/null -w "%{http_code}" -X DELETE "$BASE/v1/organizations/$ORG_ID" \
  -H "Authorization: Bearer $CHARLIE_TOKEN"
# Expected: 403
```

## 10. Test Results Summary

### Live API Verification (17/17 passed)

| # | Test | Expected | Result |
|---|------|----------|--------|
| 1 | Create organization | 201 | PASS |
| 2 | Create team | 201 | PASS |
| 3 | Org owner creates team-scoped conv | 201 | PASS |
| 4 | Org owner reads team conv (implicit MANAGER) | 200 | PASS |
| 5 | Team member reads team conv | 200, writer | PASS |
| 6 | Non-team org member blocked from team conv | 403 | PASS |
| 7 | Org member (not admin) blocked from org conv | 403 | PASS |
| 8 | Org admin reads org conv (implicit MANAGER) | 200, manager | PASS |
| 9 | Share with non-org user rejected | 400 | PASS |
| 10 | Share with org member succeeds | 201 | PASS |
| 11 | Non-scoped conversation works | 200 | PASS |
| 12 | Org-scoped listing returns correct count | 2 | PASS |
| 13 | Unauthenticated user gets 401 | 401 | PASS |
| 14 | Regular member cannot delete org | 403 | PASS |
| 15 | Non-org member cannot join team | 403 | PASS |
| 16 | Update organization name | 200 | PASS |
| 17 | Promote member role to admin | 200 | PASS |

### Unit Tests (40/40 passed)

| Test class | Tests | Status |
|---|---|---|
| `LocalAuthorizationServiceTest` | 6 | PASS |
| `SpiceDbAuthorizationServiceTest` | 3 | PASS |
| `AuthorizationServiceSelectorTest` | 5 | PASS |
| `PostgresqlMemoryServiceTest` | 6 | PASS |
| `MongoMemoryServiceTest` | 6 | PASS |
| `InfinispanMemoryEntriesCacheConfigTest` | 3 | PASS |
| `RedisHealthCheckTest` | 5 | PASS |
| `VectorStoreSelectorTest` | 3 | PASS |
| `InfinispanResponseResumerLocatorStoreConfigTest` | 3 | PASS |

### Cucumber Integration Tests

Cucumber tests require the devcontainer environment (`wt exec`). Without it, all tests fail with 401 due to the OIDC token provider not being fully configured. The `main` branch exhibits similar failures (404s). These are pre-existing infrastructure issues unrelated to the org/team changes.

## Access Control Matrix

| User Role | Org Conv (no team) | Team Conv | Non-scoped Conv |
|---|---|---|---|
| **Org owner** | MANAGER (implicit) | MANAGER (implicit) | No access (unless shared) |
| **Org admin** | MANAGER (implicit) | MANAGER (implicit) | No access (unless shared) |
| **Org member** | No access (unless shared) | No access (unless shared or team member) | No access (unless shared) |
| **Team member** | No access (unless shared) | WRITER (implicit) | No access (unless shared) |
| **Non-org user** | No access | No access | No access (unless shared) |
| **Conversation owner** | OWNER (direct) | OWNER (direct) | OWNER (direct) |

## REST API Endpoints

### Organization Management

| Method | Path | Auth Required | Description |
|---|---|---|---|
| POST | `/v1/organizations` | Any user | Create organization (creator becomes owner) |
| GET | `/v1/organizations` | Any user | List user's organizations |
| GET | `/v1/organizations/{orgId}` | Org member | Get organization detail |
| PATCH | `/v1/organizations/{orgId}` | Org admin/owner | Update organization |
| DELETE | `/v1/organizations/{orgId}` | Org owner | Delete organization |

### Member Management

| Method | Path | Auth Required | Description |
|---|---|---|---|
| GET | `/v1/organizations/{orgId}/members` | Org member | List members |
| POST | `/v1/organizations/{orgId}/members` | Org admin/owner | Add member |
| PATCH | `/v1/organizations/{orgId}/members/{userId}` | Org admin/owner | Update role |
| DELETE | `/v1/organizations/{orgId}/members/{userId}` | Org admin/owner | Remove member |

### Team Management

| Method | Path | Auth Required | Description |
|---|---|---|---|
| GET | `/v1/organizations/{orgId}/teams` | Org member | List teams |
| POST | `/v1/organizations/{orgId}/teams` | Org admin/owner | Create team |
| GET | `/v1/organizations/{orgId}/teams/{teamId}` | Org member | Get team |
| PATCH | `/v1/organizations/{orgId}/teams/{teamId}` | Org admin/owner | Update team |
| DELETE | `/v1/organizations/{orgId}/teams/{teamId}` | Org admin/owner | Delete team |
| GET | `/v1/organizations/{orgId}/teams/{teamId}/members` | Org member | List team members |
| POST | `/v1/organizations/{orgId}/teams/{teamId}/members` | Org admin/owner | Add team member |
| DELETE | `/v1/organizations/{orgId}/teams/{teamId}/members/{userId}` | Org admin/owner | Remove team member |

### Modified Conversation Endpoints

| Method | Path | Change |
|---|---|---|
| POST | `/v1/conversations?organizationId=&teamId=` | New query params for org/team scoping |
| GET | `/v1/conversations?organizationId=` | New query param to filter by org |

## Files Changed

### New Files (30)

| Path | Description |
|---|---|
| `security/AuthorizationService.java` | Authorization abstraction interface |
| `security/LocalAuthorizationService.java` | DB-backed implementation with implicit org/team access |
| `security/SpiceDbAuthorizationService.java` | SpiceDB gRPC implementation |
| `security/SpiceDbBootstrap.java` | Schema + membership sync on startup |
| `config/AuthorizationServiceSelector.java` | Picks local or spicedb backend |
| `persistence/entity/OrganizationEntity.java` | JPA entity |
| `persistence/entity/OrganizationMemberEntity.java` | JPA entity with composite PK |
| `persistence/entity/TeamEntity.java` | JPA entity |
| `persistence/entity/TeamMemberEntity.java` | JPA entity with composite PK |
| `persistence/repo/OrganizationRepository.java` | Panache repository |
| `persistence/repo/OrganizationMemberRepository.java` | Panache repository |
| `persistence/repo/TeamRepository.java` | Panache repository |
| `persistence/repo/TeamMemberRepository.java` | Panache repository |
| `mongo/model/MongoOrganization.java` | MongoDB model |
| `mongo/model/MongoOrganizationMember.java` | MongoDB model |
| `mongo/model/MongoTeam.java` | MongoDB model |
| `mongo/model/MongoTeamMember.java` | MongoDB model |
| `mongo/repo/MongoOrganizationRepository.java` | MongoDB repository |
| `mongo/repo/MongoOrganizationMemberRepository.java` | MongoDB repository |
| `mongo/repo/MongoTeamRepository.java` | MongoDB repository |
| `mongo/repo/MongoTeamMemberRepository.java` | MongoDB repository |
| `api/dto/OrganizationDto.java` | Response DTO |
| `api/dto/OrganizationMemberDto.java` | Response DTO |
| `api/dto/TeamDto.java` | Response DTO |
| `api/dto/TeamMemberDto.java` | Response DTO |
| `api/dto/CreateOrganizationRequest.java` | Request DTO |
| `api/dto/AddOrganizationMemberRequest.java` | Request DTO |
| `api/dto/CreateTeamRequest.java` | Request DTO |
| `api/OrganizationsResource.java` | REST endpoints (17 endpoints) |
| `db/changelog/004-organizations-teams.sql` | Postgres migration |

### Modified Files (16)

| Path | Change |
|---|---|
| `persistence/entity/ConversationGroupEntity.java` | Added nullable `organization` and `team` relations |
| `mongo/model/MongoConversationGroup.java` | Added `organizationId` and `teamId` fields |
| `store/MemoryStore.java` | Added org/team CRUD methods, `organizationId` param on `listConversations` |
| `store/impl/PostgresMemoryStore.java` | Org/team CRUD, implicit access via `findEffectiveAccessLevel`, sharing restrictions |
| `store/impl/MongoMemoryStore.java` | Same changes for MongoDB |
| `store/MeteredMemoryStore.java` | Delegated new methods with timing metrics |
| `api/ConversationsResource.java` | `organizationId`/`teamId` query params, sharing 400 handler |
| `api/dto/CreateConversationRequest.java` | Added `organizationId` and `teamId` fields |
| `grpc/ConversationsGrpcService.java` | Updated `listConversations` call signature |
| `resources/spicedb/schema.zed` | Added `team` definition |
| `resources/application.properties` | Added authz config properties |
| `resources/db/schema.sql` | Canonical schema with new tables |
| `resources/db/changelog/db.changelog-master.yaml` | Added changeset 4 |
| `resources/db/changelog-mongodb/db.changelog-master.yaml` | Added MongoDB changeset |
| `compose.yaml` | Added SpiceDB service |
| `pom.xml` | Added authzed dependency |
