#!/bin/bash
# Devcontainer initialization — runs on the HOST before container starts.
# Resolves workspace paths for docker-compose volume mounts.
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
WORKSPACE_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

# Ensure required host directories exist
mkdir -p "${HOME}/.m2" 2>/dev/null || true
mkdir -p "${HOME}/.claude" 2>/dev/null || true
touch "$SCRIPT_DIR/.env"

# Remove previously auto-generated vars, preserve user-defined vars
grep -v '^WORKSPACE_PARENT=\|^WORKSPACE_BASENAME=\|^USER_HOME=' "$SCRIPT_DIR/.env" > "$SCRIPT_DIR/.env.tmp" 2>/dev/null || true
mv "$SCRIPT_DIR/.env.tmp" "$SCRIPT_DIR/.env"

# Resolve paths for docker-compose volume mounts
WORKSPACE_PARENT="$(cd "$WORKSPACE_DIR/.." && pwd)"
WORKSPACE_BASENAME="$(basename "$WORKSPACE_DIR")"
USER_HOME="${HOME:-$USERPROFILE}"

cat >> "$SCRIPT_DIR/.env" <<EOF
WORKSPACE_PARENT=${WORKSPACE_PARENT}
WORKSPACE_BASENAME=${WORKSPACE_BASENAME}
USER_HOME=${USER_HOME}
EOF
