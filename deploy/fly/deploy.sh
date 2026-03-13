#!/usr/bin/env bash
set -euo pipefail

# ─────────────────────────────────────────────────────────────
# Deploy memory-service to Fly.io (free tier)
#
# Prerequisites:
#   - flyctl installed (https://fly.io/docs/flyctl/install/)
#   - Authenticated: fly auth login
#
# Usage:
#   ./deploy/fly/deploy.sh              # first-time setup + deploy
#   ./deploy/fly/deploy.sh deploy-only  # redeploy (skip infra setup)
# ─────────────────────────────────────────────────────────────

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
cd "$REPO_ROOT"

APP_NAME="${FLY_APP_NAME:-memory-service-poc}"
PG_APP_NAME="${APP_NAME}-db"
REGION="${FLY_REGION:-lhr}"

# ── Colors ──────────────────────────────────────────────────
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

info()  { echo -e "${GREEN}[+]${NC} $*"; }
warn()  { echo -e "${YELLOW}[!]${NC} $*"; }
error() { echo -e "${RED}[x]${NC} $*" >&2; }

# ── Preflight checks ───────────────────────────────────────
if ! command -v fly &>/dev/null; then
    error "flyctl not found. Install it: https://fly.io/docs/flyctl/install/"
    exit 1
fi

if ! fly auth whoami &>/dev/null; then
    error "Not authenticated. Run: fly auth login"
    exit 1
fi

info "Authenticated as: $(fly auth whoami)"

# ── Deploy-only mode (skip infra creation) ──────────────────
if [ "${1:-}" = "deploy-only" ]; then
    info "Deploying $APP_NAME (skip infra setup)..."
    fly deploy --app "$APP_NAME"
    info "Deployed! URL: https://${APP_NAME}.fly.dev"
    exit 0
fi

# ── Step 1: Create the Fly app ──────────────────────────────
if fly apps list | grep -q "^$APP_NAME "; then
    info "App '$APP_NAME' already exists, skipping creation."
else
    info "Creating app '$APP_NAME' in region '$REGION'..."
    fly apps create "$APP_NAME" --machines
fi

# ── Step 2: Create Fly Postgres ─────────────────────────────
if fly apps list | grep -q "^$PG_APP_NAME "; then
    info "Postgres app '$PG_APP_NAME' already exists, skipping creation."
else
    info "Creating Postgres cluster '$PG_APP_NAME' (free tier)..."
    fly postgres create \
        --name "$PG_APP_NAME" \
        --region "$REGION" \
        --vm-size shared-cpu-1x \
        --initial-cluster-size 1 \
        --volume-size 1
    info "Postgres cluster created."
fi

# ── Step 3: Attach Postgres to the app ──────────────────────
info "Attaching Postgres to app (sets DATABASE_URL secret)..."
fly postgres attach "$PG_APP_NAME" --app "$APP_NAME" 2>/dev/null || {
    warn "Postgres already attached or attach failed — checking if DATABASE_URL is set..."
}

# Verify DATABASE_URL is set; map it to MEMORY_SERVICE_DB_URL
if fly secrets list --app "$APP_NAME" | grep -q "DATABASE_URL"; then
    info "DATABASE_URL is set. Mapping to MEMORY_SERVICE_DB_URL..."
    # Fly sets DATABASE_URL on attach; we read it and also set our env var
    # The app reads MEMORY_SERVICE_DB_URL, so we set it from DATABASE_URL at runtime
else
    error "DATABASE_URL not found. Manually attach Postgres or set MEMORY_SERVICE_DB_URL."
    error "  fly postgres attach $PG_APP_NAME --app $APP_NAME"
    exit 1
fi

# ── Step 4: Generate and set secrets ────────────────────────
info "Setting secrets..."

# Generate a random API key if not provided
AGENT_API_KEY="${AGENT_API_KEY:-$(openssl rand -hex 24)}"

# Generate a random encryption key (32 bytes = 64 hex chars)
ENCRYPTION_KEY="${ENCRYPTION_KEY:-$(openssl rand -hex 32)}"

# Generate attachment signing secret
ATTACHMENT_SECRET="${ATTACHMENT_SECRET:-$(openssl rand -hex 32)}"

# DATABASE_URL is set automatically by `fly postgres attach`.
# The app reads DATABASE_URL as a fallback for MEMORY_SERVICE_DB_URL.
fly secrets set --app "$APP_NAME" \
    MEMORY_SERVICE_API_KEYS_AGENT="$AGENT_API_KEY" \
    MEMORY_SERVICE_ENCRYPTION_DEK_KEY="$ENCRYPTION_KEY" \
    MEMORY_SERVICE_ATTACHMENT_SIGNING_SECRET="$ATTACHMENT_SECRET"

info "Secrets set."
echo ""
warn "Save your agent API key (you won't see it again):"
echo -e "  ${GREEN}AGENT_API_KEY=${AGENT_API_KEY}${NC}"
echo ""

# ── Step 5: Deploy ──────────────────────────────────────────
info "Deploying $APP_NAME..."
fly deploy --app "$APP_NAME"

echo ""
info "Deployment complete!"
echo ""
echo "  URL:     https://${APP_NAME}.fly.dev"
echo "  API Key: $AGENT_API_KEY"
echo ""
echo "  Test it:"
echo "    curl -s https://${APP_NAME}.fly.dev/ready"
echo "    curl -s -H 'Authorization: Bearer $AGENT_API_KEY' \\"
echo "      https://${APP_NAME}.fly.dev/v1/conversations | jq ."
echo ""
echo "  Redeploy after code changes:"
echo "    ./deploy/fly/deploy.sh deploy-only"
echo ""
echo "  Logs:"
echo "    fly logs --app $APP_NAME"
echo ""
echo "  Postgres console:"
echo "    fly postgres connect --app $PG_APP_NAME"
