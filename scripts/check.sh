#!/bin/sh
# Development only. End users run the compiled executable.
set -eu
cd "$(dirname "$0")/.."
export GOCACHE="$PWD/.cache/go-build"
export GOMODCACHE="$PWD/.cache/go-mod"
export GOPATH="$PWD/.cache/gopath"
export TMPDIR="$PWD/.local/tmp"
mkdir -p "$TMPDIR"
bridge_go="${BRIDGE_GO:-go}"
"$bridge_go" fmt ./...
"$bridge_go" vet ./...
"$bridge_go" test -race ./...
node --test scripts/panel.test.mjs scripts/dev-isolation.test.mjs scripts/site.test.mjs
node --check site/site.js
node --check internal/bridge/web/app.js
node --check examples/app.js
node --check examples/server.mjs
npm run theme:check
npm run check:sdk
# Allow concurrent UI work to keep its generated assets untouched.
npm run build:sdk -- --outDir "${BRIDGE_SDK_OUT_DIR:-internal/bridge/web}"
npm run test:sdk
for bridge_doc in docs/app-definition/requirements.md docs/app-definition/security.md; do
  test "$(wc -l < "$bridge_doc")" -le 30 || { echo "$bridge_doc exceeds 30 lines"; exit 1; }
done
