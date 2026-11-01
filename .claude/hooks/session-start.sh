#!/bin/bash
# Prepares Claude Code cloud sessions: Go modules (+ toolchain) and the npm
# projects of the web client (web/) and the Playwright suite (e2e/).
set -euo pipefail

if [ "${CLAUDE_CODE_REMOTE:-}" != "true" ]; then
  exit 0
fi

cd "${CLAUDE_PROJECT_DIR:-.}"

# Go: downloads the toolchain named in go.mod (GOTOOLCHAIN=auto) and modules.
# Fills the module cache outside the repo, so it runs every time (no-op when cached).
go mod download

# npm projects: install only when the manifest/lockfile (or this script) changed.
for dir in web e2e; do
  hashes="$dir/node_modules/.session-start.sha256"
  if ! sha256sum --check --status "$hashes" 2>/dev/null; then
    rm -f "$hashes"
    (cd "$dir" && npm install --no-audit --no-fund)
    sha256sum .claude/hooks/session-start.sh "$dir/package.json" "$dir/package-lock.json" 2>/dev/null > "$hashes" || rm -f "$hashes"
  fi
done

# Playwright uses the browsers preinstalled in the cloud image.
if [ -n "${CLAUDE_ENV_FILE:-}" ] && [ -d /opt/pw-browsers ]; then
  echo 'export PLAYWRIGHT_BROWSERS_PATH=/opt/pw-browsers' >> "$CLAUDE_ENV_FILE"
fi
