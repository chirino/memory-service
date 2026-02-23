#!/bin/sh
# This script runs inside the Node Docker container (see pom.xml).
# The project directory is mounted at /app, which is also the working directory.
set -e

# Up-to-date check: skip build if dist/index.html is newer than all source files
# (mirrors the old Ant <uptodate> logic: excludes dist/, target/, node_modules/, package-lock.json)
if [ -f dist/index.html ] && \
   [ -z "$(find . -type f \
       ! -path './dist/*' \
       ! -path './target/*' \
       ! -path './node_modules/*' \
       ! -name 'package-lock.json' \
       -newer dist/index.html 2>/dev/null | head -1)" ]; then
  echo "[INFO] Frontend dist is up to date, skipping build"
  exit 0
fi

# Log which files triggered the rebuild
NEWER=$(find . -type f \
    ! -path './dist/*' \
    ! -path './target/*' \
    ! -path './node_modules/*' \
    ! -name 'package-lock.json' \
    -newer dist/index.html 2>/dev/null | head -30 | sed 's|^\./||')
if [ -n "$NEWER" ]; then
  echo "[DEBUG] Newer source files:"
  echo "$NEWER"
else
  echo "[DEBUG] dist/index.html missing — full build required"
fi
echo "[DEBUG] ==========================================="

npm install
npm run build

# Touch the target so the next up-to-date check recognises this build
touch dist/index.html
