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
- Generate and set secrets (API key, encryption key, attachment signing secret)
- Build and deploy the Docker image

Save the **agent API key** printed at the end — you'll need it to authenticate.

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

The deploy script forwards **all `MEMORY_SERVICE_*` environment variables** as Fly secrets. See the [Configuration docs](../../site/src/pages/docs/configuration.mdx) for the full list.

The following keys are generated automatically if not provided:

| Variable | Default | Description |
|----------|---------|-------------|
| `MEMORY_SERVICE_API_KEYS_AGENT` | random | Agent API key for authentication |
| `MEMORY_SERVICE_ENCRYPTION_DEK_KEY` | random | 32-byte hex encryption key |
| `MEMORY_SERVICE_ATTACHMENT_SIGNING_SECRET` | random | Attachment signing secret |

Example:
```bash
FLY_APP_NAME=my-team-memory FLY_REGION=lhr ./deploy/fly/deploy.sh
```

Example with extra configuration:
```bash
MEMORY_SERVICE_API_KEYS_AGENT=my-key \
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
