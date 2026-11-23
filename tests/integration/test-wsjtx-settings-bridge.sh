#!/usr/bin/env bash
# Compile and exercise the real helper, controller and cross-user fixtures inside Docker.
set -euo pipefail
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
docker_bin=${DXB_DOCKER:-docker}
docker_host=${DXB_DOCKER_HOST:-unix:///var/run/docker.sock}
go_image=${DXB_BRIDGE_GO_IMAGE:-golang:1.24.4-bookworm}
run_image=${DXB_BRIDGE_RUN_IMAGE:-debian:bookworm-slim}
# Warm checksum-verified modules before the network-isolated compilation.
module_cache=${DXB_BRIDGE_GOPATH:-$work/go}
mkdir -p "$module_cache"
"$docker_bin" --host="$docker_host" run --rm --user "$(id -u):$(id -g)" \
  -v "$root/station:/src:ro" -v "$module_cache:/cache" -w /src \
  -e HOME=/tmp -e GOPATH=/cache -e GOCACHE=/tmp/go-cache -e GOTOOLCHAIN=local "$go_image" \
  go mod download
"$docker_bin" --host="$docker_host" run --rm --network none --user "$(id -u):$(id -g)" \
  -v "$root/station:/src:ro" -v "$work:/out" -v "$module_cache:/cache:ro" -w /src \
  -e HOME=/tmp -e GOPATH=/cache -e GOCACHE=/tmp/go-cache \
  -e CGO_ENABLED=0 -e GOTOOLCHAIN=local -e GOMAXPROCS=4 "$go_image" \
  bash -c 'go test -c -o /out/wsjtx-bridge.test ./internal/wsjtx && go build -trimpath -o /out/dxberry-wsjtx-settings ./cmd/dxberry-wsjtx-settings'
"$docker_bin" --host="$docker_host" run --rm --network none \
  -v "$work:/test:ro" -e DXB_SETTINGS_HELPER_BINARY=/test/dxberry-wsjtx-settings \
  -v "$root/station/internal/wsjtx/testdata:/fixtures/testdata:ro" -w /fixtures \
  "$run_image" /test/wsjtx-bridge.test -test.v -test.run '^Test(SettingsBridge|ProgramController|SystemdProgramLifecycle|ProgramService|ProgramHooks|ProgramState|ProgramEvents|Release)'
