#!/usr/bin/env bash
# End-to-end test: builds the web client and server, generates test media,
# starts the server on a free port with a temp data dir and runs Playwright
# (desktop, iPhone-emulated and webOS-emulated projects).
set -euo pipefail
cd "$(dirname "$0")/.."
ROOT="$PWD"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/hs-e2e.XXXXXX")"
trap 'kill ${SERVER_PID:-0} 2>/dev/null || true; wait ${SERVER_PID:-0} 2>/dev/null || true' EXIT

(cd web && { [ -d node_modules ] || npm ci --no-audit --no-fund; } && npm run build)
CGO_ENABLED=0 go build -o "$WORK/homestreaming" ./cmd/homestreaming
./scripts/make-test-media.sh "$WORK/media/Movies-and-TV"

PORT="$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1])')"
HS_UPDATE_CHECK=0 "$WORK/homestreaming" serve --data-dir "$WORK/data" --addr "127.0.0.1:$PORT" --hwaccel none >"$WORK/server.log" 2>&1 &
SERVER_PID=$!
for _ in $(seq 1 50); do
  curl -sf "http://127.0.0.1:$PORT/api/v1/system/ping" >/dev/null && break
  sleep 0.2
done

export HS_BASE_URL="http://127.0.0.1:$PORT"
export HS_MEDIA_DIR="$WORK/media/Movies-and-TV"
export HS_SHOTS="${HS_SHOTS:-$ROOT/e2e/test-results/shots}"
if [ -z "${PLAYWRIGHT_BROWSERS_PATH:-}" ] && [ -d /opt/pw-browsers ]; then
  export PLAYWRIGHT_BROWSERS_PATH=/opt/pw-browsers   # preinstalled in Claude Code cloud sessions
fi
cd e2e
[ -d node_modules ] || npm ci --no-audit --no-fund || npm install --no-audit --no-fund
rm -f state.json
set +e
npx playwright test "$@"
status=$?
set -e
if [ $status -ne 0 ]; then
  echo "---- server log (tail) ----"
  tail -n 80 "$WORK/server.log"
fi
exit $status
