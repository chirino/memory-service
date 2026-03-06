# Chat Spring

This is an example agent application that demonstrates the memory-service features using Spring Boot and Spring AI.

## Configuration

### Frontend OIDC Settings

The backend serves a `/config.json` endpoint that the React frontend fetches at startup to discover the OIDC provider. This allows the OIDC endpoint to be configured at deployment time via environment variables rather than baked into the frontend build.

| Environment Variable | Property | Default | Description |
|---|---|---|---|
| `KEYCLOAK_FRONTEND_URL` | `chat.frontend.keycloak-url` | `http://localhost:8081` | Keycloak URL as seen by the browser |
| `KEYCLOAK_REALM` | `chat.frontend.keycloak-realm` | `memory-service` | Keycloak realm name |
| `KEYCLOAK_CLIENT_ID` | `chat.frontend.keycloak-client-id` | `frontend` | OIDC client ID for the SPA |

`KEYCLOAK_FRONTEND_URL` is separate from the backend's `KEYCLOAK_URL` (used for JWT issuer validation) because in containerized deployments the backend reaches Keycloak over an internal network (e.g. `http://keycloak:8080`) while the browser needs the externally reachable URL.

The frontend automatically disables PKCE when served over plain HTTP (where `Crypto.subtle` is unavailable).
