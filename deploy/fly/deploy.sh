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
    fly deploy --config ./deploy/fly/fly.toml --app "$APP_NAME"
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

# ── Step 4: Generate and set secrets ────────────────────────
info "Setting secrets..."
set -ex

# Generate a random API key if not provided
export MEMORY_SERVICE_API_KEYS_AGENT="${MEMORY_SERVICE_API_KEYS_AGENT:-$(openssl rand -hex 24)}"

# Generate a random encryption key (32 bytes = 64 hex chars)
export MEMORY_SERVICE_ENCRYPTION_DEK_KEY="${MEMORY_SERVICE_ENCRYPTION_DEK_KEY:-$(openssl rand -hex 32)}"

# Generate attachment signing secret
export MEMORY_SERVICE_ATTACHMENT_SIGNING_SECRET="${MEMORY_SERVICE_ATTACHMENT_SIGNING_SECRET:-$(openssl rand -hex 32)}"

SECRETS=()
while IFS='=' read -r key value; do
    SECRETS+=("$key=$value")
done < <(env | grep '^MEMORY_SERVICE_')
fly secrets set --app "$APP_NAME" "${SECRETS[@]}"

info "Secrets set."
echo ""
warn "Save your agent API key (you won't see it again):"
echo -e "  ${GREEN}MEMORY_SERVICE_API_KEYS_AGENT=${MEMORY_SERVICE_API_KEYS_AGENT}${NC}"
echo ""

# # ── Step 5: Deploy ──────────────────────────────────────────
info "Deploying $APP_NAME..."
fly deploy --app "$APP_NAME" --config ./deploy/fly/fly.toml

echo ""
info "Deployment complete!"
echo ""
echo "  URL:     https://${APP_NAME}.fly.dev"
echo "  API Key: $MEMORY_SERVICE_API_KEYS_AGENT"
echo ""
echo "  Test it:"
echo "    curl -s https://${APP_NAME}.fly.dev/ready"
echo "    curl -s -H 'Authorization: Bearer $MEMORY_SERVICE_API_KEYS_AGENT' \\"
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
