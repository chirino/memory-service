# Deploying memory-service to Fly.io

Minimal deployment on Fly.io free tier: Go server + Postgres, API key auth, no Redis/Qdrant/OIDC.

## Prerequisites

1. Install [flyctl](https://fly.io/docs/flyctl/install/)
2. Authenticate: `fly auth login`

## First-time deploy

```bash
./deploy/fly/deploy.sh
```

This will:
- Create a Fly app (`memory-service-poc`)
- Provision a free-tier Postgres cluster (`memory-service-db`)
- Generate and set secrets (API key, encryption key, attachment signing secret)
- Build and deploy the Docker image

Save the **agent API key** printed at the end — you'll need it to authenticate.

## Redeploy after code changes

```bash
./deploy/fly/deploy.sh deploy-only
```

## Customization

Set environment variables before running the script:

| Variable | Default | Description |
|----------|---------|-------------|
| `FLY_APP_NAME` | `memory-service-poc` | Fly app name |
| `FLY_REGION` | `lhr` | Fly region (London) |
| `AGENT_API_KEY` | random | Use a specific API key |
| `ENCRYPTION_KEY` | random | 32-byte hex encryption key |
| `ATTACHMENT_SECRET` | random | Attachment signing secret |

Example:
```bash
FLY_APP_NAME=my-team-memory FLY_REGION=lhr ./deploy/fly/deploy.sh
```

## What's included

| Feature | Status |
|---------|--------|
| Conversations API | Enabled |
| API key auth | Enabled |
| Postgres datastore | Enabled (auto-migrates) |
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

# Postgres console
fly postgres connect --app memory-service-poc-db

# SSH into the app
fly ssh console --app memory-service-poc

# Scale (if you outgrow free tier)
fly scale memory 512 --app memory-service-poc

# Destroy everything
fly apps destroy memory-service-poc
fly apps destroy memory-service-poc-db
```
