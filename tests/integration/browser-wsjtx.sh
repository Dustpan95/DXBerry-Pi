#!/bin/bash
# Read-only native program observations over the real events socket, without hardware.
set -uo pipefail
cd "$(dirname "$0")/../.." || exit 1
export DXB_BROWSER_OUTPUT=${DXB_BROWSER_OUTPUT:-/tmp/dxberry-wsjtx-browser}
mkdir -p "$DXB_BROWSER_OUTPUT" || exit 1
node tests/integration/browser-wsjtx.js status || exit 1
DXB_BROWSER_FIXTURE=1 DXB_BROWSER_PROGRAM=wsjtx DXB_BROWSER_STATUS="$DXB_BROWSER_OUTPUT/status.json" \
  "${DXB_BROWSER_BIN:-/usr/local/bin/dxberry-browser-fixture}" -test.run='^TestBrowserFixture$' -test.v \
  > "$DXB_BROWSER_OUTPUT/server.log" 2>&1 &
fixture_pid=$!
trap 'kill "$fixture_pid" 2>/dev/null || true' EXIT
ready=0
for ((attempt=0; attempt<100; attempt++)); do
  if curl -fsS http://127.0.0.1:8090/api/version >/dev/null 2>&1; then ready=1; break; fi
  kill -0 "$fixture_pid" 2>/dev/null || break
  sleep 0.1
done
if (( ! ready )); then cat "$DXB_BROWSER_OUTPUT/server.log" >&2; exit 1; fi
node tests/integration/browser-wsjtx.js
browser_rc=$?
curl -fsS -X POST http://127.0.0.1:8090/fixture/done >/dev/null 2>&1 || true
wait "$fixture_pid"
server_rc=$?
if (( browser_rc || server_rc )); then cat "$DXB_BROWSER_OUTPUT/server.log" >&2; exit 1; fi
