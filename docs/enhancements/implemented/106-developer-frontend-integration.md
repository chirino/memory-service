---
status: implemented
---

# Enhancement 106: Developer Frontend Integration

> **Status**: Implemented. The Go service can serve the developer frontend under `/developer`, expose `/developer/config.json`, package built assets in the container image, and document the runtime flags. Focused route unit tests exist; BDD coverage remains a follow-up gap.

## Summary

Serve the existing developer frontend SPA from the Go memory-service binary under `/developer/*` when explicitly enabled. The backend provides a same-origin runtime config endpoint at `/developer/config.json` and serves the built Vite assets from `frontends/developer/dist`.

## Motivation

The developer frontend currently runs as a separate Vite/static deployment and needs its own runtime OIDC configuration. That is useful for frontend development, but it adds friction for local backend development and simple internal deployments.

This enhancement adds an optional integrated mode:

- one memory-service process can serve both APIs and the developer UI
- `/developer/config.json` is generated from backend OIDC/listener settings
- browser API calls are same-origin, so production CORS configuration is not required for the integrated UI
- the served UI inherits the backend listener's TLS behavior, while API authorization remains enforced by the existing Admin API

## Scope

In scope:

- `GET /developer` and `GET /developer/`
- `GET /developer/config.json`
- `GET /developer/assets/*`
- SPA fallback for client-side routes such as `/developer/conversations/{id}`
- startup validation when the feature is enabled
- local and container build wiring needed to make the configured asset directory exist

Out of scope:

- health-check changes
- management endpoint changes
- custom theming
- multi-frontend support
- runtime frontend compilation
- frontend hot reload through the Go server
- `go:embed` asset embedding

## Current State

The developer frontend already exists in `frontends/developer` and uses Vite with `base: '/developer/'`. Its runtime config loader in `frontends/developer/src/lib/config.ts` fetches `${import.meta.env.BASE_URL}config.json`, so integrated serving must return a nested JSON object from `/developer/config.json`:

```json
{
  "apiUrl": "http://localhost:8082",
  "oidc": {
    "authority": "http://localhost:8081/realms/memory-service",
    "clientId": "developer-frontend",
    "redirectUri": "http://localhost:8082/developer/"
  }
}
```

The Go Admin API already authorizes read access with `security.RequireAuditorRole()`; admin users are also auditors because `TokenResolver.Resolve` adds auditor when admin is present. The developer frontend itself uses `RequireAuth` in `frontends/developer/src/lib/auth.tsx` to require the `admin` or `auditor` role after login.

The static shell and `/developer/config.json` must be served without backend bearer-token auth. The SPA needs those files before it can discover OIDC settings and start the login flow. The runtime config contains public OIDC/client metadata only; protected data remains behind the existing Admin API.

## Design

### Configuration

Add fields to `internal/config/config.go`:

```go
// Developer frontend configuration.
DeveloperFrontendEnabled  bool
DeveloperFrontendDir      string
DeveloperFrontendClientID string
BaseURL                   string
```

Defaults:

```go
DeveloperFrontendEnabled:  false,
DeveloperFrontendDir:      "./frontends/developer/dist",
DeveloperFrontendClientID: "developer-frontend",
```

Add `serve` flags in `internal/cmd/serve/serve.go`:

```go
&cli.BoolFlag{
    Name:        "developer-frontend-enabled",
    Category:    "Developer Frontend:",
    Sources:     cli.EnvVars("MEMORY_SERVICE_DEVELOPER_FRONTEND_ENABLED"),
    Destination: &cfg.DeveloperFrontendEnabled,
    Value:       cfg.DeveloperFrontendEnabled,
    Usage:       "Enable serving the developer frontend SPA under /developer",
}

&cli.StringFlag{
    Name:        "developer-frontend-dir",
    Category:    "Developer Frontend:",
    Sources:     cli.EnvVars("MEMORY_SERVICE_DEVELOPER_FRONTEND_DIR"),
    Destination: &cfg.DeveloperFrontendDir,
    Value:       cfg.DeveloperFrontendDir,
    Usage:       "Directory containing built developer frontend assets",
}

&cli.StringFlag{
    Name:        "developer-frontend-client-id",
    Category:    "Developer Frontend:",
    Sources:     cli.EnvVars("MEMORY_SERVICE_DEVELOPER_FRONTEND_CLIENT_ID"),
    Destination: &cfg.DeveloperFrontendClientID,
    Value:       cfg.DeveloperFrontendClientID,
    Usage:       "OIDC public client ID for the developer frontend",
}

&cli.StringFlag{
    Name:        "base-url",
    Category:    "Developer Frontend:",
    Sources:     cli.EnvVars("MEMORY_SERVICE_BASE_URL"),
    Destination: &cfg.BaseURL,
    Usage:       "External base URL for redirects and runtime config; defaults to advertised address or listener",
}
```

### Route Registration

Create `internal/plugin/route/developer/developer.go` and call it from `BuildServer` after `registerAPIRoutes(...)`:

```go
if cfg.DeveloperFrontendEnabled {
    if err := developer.RegisterRoutes(router, cfg); err != nil {
        return nil, fmt.Errorf("failed to register developer frontend routes: %w", err)
    }
    log.Info("Developer frontend enabled", "path", "/developer", "dir", cfg.DeveloperFrontendDir)
}
```

The route package should expose:

```go
func RegisterRoutes(router *gin.Engine, cfg *config.Config) error
```

Registration requirements:

- return immediately when `DeveloperFrontendEnabled` is false
- fail startup when `DeveloperFrontendDir` does not exist
- fail startup when `index.html` is missing from the directory
- do not attach `security.AuthMiddleware` to the static/config routes; the SPA must be able to load before login
- register both `/developer` and `/developer/` so the bare path works
- serve `/developer/config.json` before the wildcard route
- serve `GET /developer/*filepath` with SPA fallback

Authorization remains on the Admin API calls made by the loaded frontend. Do not add a second static-file authorization scheme in this enhancement.

### Runtime Config

`GET /developer/config.json` returns the nested shape consumed by `frontends/developer/src/lib/config.ts`:

```go
gin.H{
    "apiUrl": baseURL,
    "oidc": gin.H{
        "authority":   cfg.OIDCIssuer,
        "clientId":    cfg.DeveloperFrontendClientID,
        "redirectUri": strings.TrimRight(baseURL, "/") + "/developer/",
    },
}
```

Set:

```http
Content-Type: application/json
Cache-Control: no-cache, no-store, must-revalidate
X-Content-Type-Options: nosniff
```

Base URL resolution order:

1. `cfg.BaseURL`, if set
2. `cfg.ResumerAdvertisedAddress`, with scheme derived from `cfg.Listener.EnableTLS`
3. listener fallback using `localhost:{cfg.Listener.Port}` and the listener TLS scheme

Trim trailing slashes before appending `/developer/`.

### Static File Serving

The file handler must keep all resolved paths inside `DeveloperFrontendDir`:

```go
func resolveAssetPath(distDir, requestPath string) (string, bool) {
    clean := path.Clean("/" + requestPath)
    relative := strings.TrimPrefix(clean, "/")
    if relative == "" {
        relative = "index.html"
    }

    fullPath := filepath.Join(distDir, filepath.FromSlash(relative))
    rel, err := filepath.Rel(distDir, fullPath)
    if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || rel == ".." {
        return "", false
    }
    return fullPath, true
}
```

Behavior:

- `/developer` serves `index.html`
- `/developer/` serves `index.html`
- existing asset files are served directly
- missing paths without a file extension fall back to `index.html`
- missing paths with a file extension return `404`, so typos under `/developer/assets/*` do not silently return HTML
- only `GET` is required

Cache headers:

- `index.html` and `/developer/config.json`: `no-cache, no-store, must-revalidate`
- files under `/developer/assets/`: `public, max-age=31536000, immutable`
- other static files: `no-cache`

Security headers for static responses:

```http
Content-Security-Policy: default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; img-src 'self' data: blob:; font-src 'self' data: https://fonts.gstatic.com; connect-src 'self' {oidc-origin}; frame-ancestors 'none'
X-Frame-Options: DENY
X-Content-Type-Options: nosniff
Referrer-Policy: strict-origin-when-cross-origin
```

Build `{oidc-origin}` from `cfg.OIDCIssuer` when it is configured. The browser OIDC client may fetch discovery or token endpoints from the issuer origin, which is commonly different from the memory-service origin in local development. If no issuer is configured, omit the extra origin and keep `connect-src 'self'`.

`style-src 'unsafe-inline'` is allowed in this initial integration because the current frontend build uses normal Vite/Tailwind output and this enhancement is focused on making `/developer/*` work. Nonce-based CSP can be handled by a later hardening enhancement if needed.

Do not add `X-XSS-Protection`; the header is obsolete in modern browsers.

### CORS

No production CORS change is required for integrated serving because the UI and APIs are same-origin. For separate Vite development, continue to use the existing `MEMORY_SERVICE_CORS_ENABLED` and `MEMORY_SERVICE_CORS_ORIGINS` settings. The current `task dev:memory-service` already sets `MEMORY_SERVICE_CORS_ORIGINS=http://localhost:3000`.

### OAuth Client

The integrated frontend uses the same OIDC issuer as the backend and a separate public frontend client ID:

- issuer: `cfg.OIDCIssuer`
- client ID: `cfg.DeveloperFrontendClientID`
- redirect URI: `{baseURL}/developer/`
- flow: Authorization Code + PKCE through `oidc-client-ts`

Backend token validation already skips client ID checks and resolves admin/auditor roles from the configured role names, user allowlists, and client allowlists. No backend OIDC verifier change is required.

Because the static shell is public, do not place secrets in `/developer/config.json`. OIDC public client IDs, issuer URLs, and redirect URIs are safe to expose.

Keycloak example:

```bash
kcadm.sh create clients -r memory-service \
  -s clientId=developer-frontend \
  -s publicClient=true \
  -s standardFlowEnabled=true \
  -s directAccessGrantsEnabled=false \
  -s 'redirectUris=["http://localhost:8082/developer/*"]' \
  -s 'webOrigins=["http://localhost:8082"]'
```

### Build And Packaging

Local development can either continue to run the Vite dev server or build static assets for integrated serving:

```bash
cd frontends/developer
npm ci
npm run build
cd ../..

go build .
./memory-service serve \
  --developer-frontend-enabled \
  --developer-frontend-dir=./frontends/developer/dist \
  --oidc-issuer=http://localhost:8081/realms/memory-service \
  --advertised-address=localhost:8082 \
  --port=8082
```

Add a focused Taskfile task for static assets:

```yaml
dev:developer-frontend:
  desc: Build developer frontend static assets
  dir: frontends/developer
  cmds:
    - npm ci
    - npm run build
```

Do not make `dev:memory-service` depend on the frontend build by default; that command should continue to be useful for backend-only development.

For the container image, add a frontend builder stage and copy `dist` into the runtime image. Because the default directory is relative, set `WORKDIR /app` and copy to `/app/frontends/developer/dist`.

## Implementation Plan

### Phase 1: `/developer/*` Route Integration

- [x] Add config fields and defaults.
- [x] Add serve flags and env var bindings.
- [x] Create `internal/plugin/route/developer/developer.go`.
- [x] Register `/developer` routes from `BuildServer`.
- [x] Implement `/developer/config.json`.
- [x] Serve `index.html`, static assets, and extensionless SPA fallback.
- [x] Keep static/config routes unauthenticated so the OIDC login flow can start.
- [x] Add focused unit tests for config JSON, static file serving, SPA fallback, missing asset 404, startup validation, and path handling.
- [ ] Add BDD coverage for enabled/disabled route behavior and static/config serving.

### Phase 2: Build And Packaging Wiring

- [x] Add `dev:developer-frontend` task for frontend development.
- [x] Update Dockerfile to build and copy `frontends/developer/dist`.
- [x] Set `MEMORY_SERVICE_DEVELOPER_FRONTEND_ENABLED=true` in local compose/dev examples while keeping the feature explicit in normal configuration.
- [x] Document OIDC client registration and the integrated URL.

## Testing Strategy

### BDD Scenarios

```gherkin
Feature: Developer Frontend Integration

  Scenario: Developer frontend is disabled by default
    Given the memory service is running without developer frontend enabled
    When I GET "/developer/"
    Then the response status should be 404

  Scenario: Config endpoint returns runtime frontend configuration
    Given the memory service is running with developer frontend enabled
    And OIDC is configured with issuer "http://keycloak:8080/realms/test"
    When I GET "/developer/config.json"
    Then the response status should be 200
    And the JSON field "apiUrl" should match the configured base URL
    And the JSON field "oidc.authority" should be "http://keycloak:8080/realms/test"
    And the JSON field "oidc.clientId" should be "developer-frontend"
    And the JSON field "oidc.redirectUri" should end with "/developer/"

  Scenario: Index route serves the SPA shell
    Given the memory service is running with developer frontend enabled
    When I GET "/developer/"
    Then the response status should be 200
    And the response header "Content-Security-Policy" should be present
    And the response should contain "<!doctype html>"

  Scenario: Bare developer path serves the SPA shell
    Given the memory service is running with developer frontend enabled
    When I GET "/developer"
    Then the response status should be 200
    And the response should contain "<!doctype html>"

  Scenario: SPA routing fallback works for extensionless paths
    Given the memory service is running with developer frontend enabled
    When I GET "/developer/conversations/123"
    Then the response status should be 200
    And the response should contain "<!doctype html>"

  Scenario: Missing asset returns not found
    Given the memory service is running with developer frontend enabled
    When I GET "/developer/assets/missing.js"
    Then the response status should be 404

  Scenario: Static shell can load before authentication
    Given the memory service is running with developer frontend enabled
    When I GET "/developer/" without an Authorization header
    Then the response status should be 200

  Scenario: Missing frontend assets fail startup
    Given the developer frontend is enabled
    And the frontend directory does not contain "index.html"
    When I start the memory service
    Then startup should fail with error containing "developer frontend index.html not found"
```

### Unit Tests

- `configHandler` returns the nested config shape expected by `frontends/developer/src/lib/config.ts`.
- base URL derivation trims trailing slashes and appends `/developer/` exactly once.
- asset path resolution rejects traversal attempts.
- extensionless missing paths fall back to `index.html`.
- missing paths with extensions return `404`.

### Verification Commands

```bash
cd frontends/developer && npm ci && npm run build && cd ../..

go build .

./memory-service serve \
  --developer-frontend-enabled \
  --developer-frontend-dir=./frontends/developer/dist \
  --oidc-issuer=http://localhost:8081/realms/memory-service \
  --advertised-address=localhost:8082 \
  --port=8082

curl http://localhost:8082/developer/config.json
curl -I http://localhost:8082/developer/
curl -I http://localhost:8082/developer/conversations/example
```

After Go changes, run the affected Go tests. If the route package includes BDD coverage, run the matching BDD runner as well.

## Alternatives Considered

### Separate Static Deployment

Keep the frontend as a separate Vite/Nginx/CDN deployment.

This remains useful for independent frontend development and production environments that already have static hosting, but it does not solve the local and simple internal deployment use case.

### Root Path Serving

Serve the frontend at `/`.

This was rejected because it conflicts with API route organization and makes API/UI traffic harder to reason about. `/developer` matches the frontend's existing Vite base path.

### Embedded Assets With `go:embed`

Embed `dist` in the Go binary.

This is out of scope for this enhancement. File-based serving is simpler to implement and keeps frontend rebuilds independent from Go rebuilds.

## Files To Modify

| File | Change |
|------|--------|
| `internal/config/config.go` | Add developer frontend fields and defaults |
| `internal/cmd/serve/serve.go` | Add developer frontend flags |
| `internal/cmd/serve/server.go` | Register developer routes when enabled |
| `internal/plugin/route/developer/developer.go` | New public static/config `/developer/*` route implementation |
| `internal/plugin/route/developer/developer_test.go` | Unit tests for config, path resolution, headers, and fallback behavior |
| `internal/bdd/testdata/features/developer-frontend-rest.feature` | BDD coverage for enabled/disabled route behavior and static/config serving |
| `frontends/developer/FACTS.md` | Keep runtime config documentation aligned with the flat `/developer/config.json` shape |
| `Dockerfile` | Build/copy `frontends/developer/dist` for containerized integrated serving |
| `Taskfile.yml` | Add an explicit `dev:developer-frontend` build task |
| `docs/configuration.mdx` | Document flags and integrated serving example |

## Open Questions

None. The remaining choices are implementation details, not product or API blockers.
