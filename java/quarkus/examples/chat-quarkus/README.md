# Agent

This is an example agent application that demonstrates the memory-service features and evaluates
how easily its capabilities can be consumed by a Quarkus LangChain4j agent.

We should strive to move as much boilerplate code as possible from this module into the memory-service-extension.

## Configuration

### Frontend OIDC Settings

The backend serves a `/config.json` endpoint that the React frontend fetches at startup to discover the OIDC provider. This allows the OIDC endpoint to be configured at deployment time via environment variables rather than baked into the frontend build.

| Environment Variable | Property | Default | Description |
|---|---|---|---|
| `KEYCLOAK_FRONTEND_URL` | `chat.frontend.keycloak-url` | `http://localhost:8081` | Keycloak URL as seen by the browser |
| `KEYCLOAK_REALM` | `chat.frontend.keycloak-realm` | `memory-service` | Keycloak realm name |
| `KEYCLOAK_CLIENT_ID` | `chat.frontend.keycloak-client-id` | `frontend` | OIDC client ID for the SPA |

`KEYCLOAK_FRONTEND_URL` is separate from the backend's OIDC auth-server-url because in containerized deployments the backend reaches Keycloak over an internal network (e.g. `http://keycloak:8080`) while the browser needs the externally reachable URL.

The frontend automatically disables PKCE when served over plain HTTP (where `Crypto.subtle` is unavailable).

### Cognition Memory RAG

`chat-quarkus` can enrich LLM requests with Memory Service cognition memories. The feature is disabled by default and enabled in the `alt` profile used with an external Memory Service and cognition processor:

```bash
task dev:memory-service
./java/mvnw -f java/pom.xml -pl :chat-quarkus quarkus:dev -Dquarkus.profile=alt
```

When enabled, the first turn of a conversation fetches `profile_context/latest` from `["user", <userId>, "cognition.v1", "profile_context"]`. Every turn also searches the user's cognition namespace and injects only close semantic matches; `profile_context` and `profile_input` memories are excluded from ad hoc injection.

The repo-root `compose.yaml` mounts `deploy/episodic-policies/cognition` into Memory Service and sets `MEMORY_SERVICE_EPISODIC_POLICY_DIR` so cognition-safe attributes such as `memoryKind` and confidence buckets can be extracted for search.

The frontend can display and edit profile context inputs through narrow chat-app endpoints:

- `GET /v1/profile-context`
- `GET /v1/profile-context/inputs`
- `PUT /v1/profile-context/inputs`

The generic `/v1/memories` API remains backend-only in this example. User-authored profile inputs are stored as one memory at `["user", <userId>, "cognition.v1", "profile_input"]` key `latest`, with the ordered strings in `value.inputs`.
