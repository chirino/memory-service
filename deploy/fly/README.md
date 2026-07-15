# Deploying memory-service to Fly.io

Minimal deployment on Fly.io free tier: memory-service server + API key auth, no Redis/Qdrant/OIDC.

## Prerequisites

1. Install [flyctl](https://fly.io/docs/flyctl/install/)
2. Authenticate: `fly auth login`

## First-time deploy

```bash
./deploy/fly/deploy.sh
```

This will:
- Create a Fly app (`memory-service-poc`)
- Generate and set required secrets (API key and encryption key)
- Build and deploy the Docker image

Generated secrets are not printed to the terminal. When the script generates values, it
writes them to `deploy/fly/.env` with mode `0600`; save that file securely because the
agent API key is required for authentication and the encryption key is required to read
existing encrypted data.

## Redeploy after code changes

```bash
./deploy/fly/deploy.sh deploy-only
```

## Customization

### Script variables

| Variable | Default | Description |
|----------|---------|-------------|
| `FLY_APP_NAME` | `memory-service-poc` | Fly app name |
| `FLY_REGION` | `lhr` | Fly region (London) |

### Memory Service configuration

The deploy script always sets the required memory-service secrets below. It does not forward
every `MEMORY_SERVICE_*` variable from your shell, to avoid accidentally storing unrelated
local values as Fly secrets.

To forward additional memory-service secrets, set `FLY_MEMORY_SERVICE_SECRET_NAMES` to a
comma- or space-separated list of exact environment variable names, and set each named
variable in the environment before running the script.

The following keys are generated automatically if not provided:

| Variable | Default | Description |
|----------|---------|-------------|
| `MEMORY_SERVICE_API_KEYS_AGENT` | random | Agent API key for authentication |
| `MEMORY_SERVICE_ENCRYPTION_DEK_KEY` | random | 32-byte hex encryption key |

Example:
```bash
FLY_APP_NAME=my-team-memory FLY_REGION=lhr ./deploy/fly/deploy.sh
```

Example with extra configuration:
```bash
MEMORY_SERVICE_API_KEYS_AGENT=my-key \
  MEMORY_SERVICE_EMBEDDING_OPENAI_API_KEY=sk-... \
  FLY_MEMORY_SERVICE_SECRET_NAMES=MEMORY_SERVICE_EMBEDDING_OPENAI_API_KEY \
  ./deploy/fly/deploy.sh
```

## What's included

| Feature | Status |
|---------|--------|
| Conversations API | Enabled |
| API key auth | Enabled |
| SQLite datastore | Enabled (auto-migrates) |
| Attachment storage | Enabled (in DB) |
| Redis caching | Disabled (not needed for small scale) |
| Vector search (Qdrant) | Disabled |
| Embeddings (OpenAI) | Disabled |
| OIDC (Keycloak) | Disabled |

## Usage

```bash
# Health check
curl -s https://memory-service-poc.fly.dev/ready

# List conversations
curl -s -H 'Authorization: Bearer <API_KEY>' \
  https://memory-service-poc.fly.dev/v1/conversations | jq .

# Create a conversation and append an entry
curl -s -X POST -H 'Authorization: Bearer <API_KEY>' \
  -H 'Content-Type: application/json' \
  -d '{"entries":[{"role":"user","content":"Hello!"}]}' \
  https://memory-service-poc.fly.dev/v1/conversations | jq .
```

## Operations

```bash
# View logs
fly logs --app memory-service-poc

# SSH into the app
fly ssh console --app memory-service-poc

# Scale (if you outgrow free tier)
fly scale memory 512 --app memory-service-poc

# Destroy everything
fly apps destroy memory-service-poc
```
