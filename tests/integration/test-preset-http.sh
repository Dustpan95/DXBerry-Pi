#!/usr/bin/env bash
# Compile and run the actual-helper HTTP fixture in hardware-free Docker.
set -euo pipefail
umask 077
if [[ $# != 2 || ! -f $1 || ! -x $1 || -L $1 ]]; then
  echo 'Usage: test-preset-http.sh NATIVE_PRESET_BINARY EMPTY_EVIDENCE_DIRECTORY' >&2
  exit 2
fi
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
binary=$(cd -- "$(dirname -- "$1")" && printf '%s/%s' "$PWD" "$(basename -- "$1")")
module_cache=${DXB_PRESET_GOPATH:-}
if [[ -z $module_cache || ! -d $module_cache ]]; then
  echo 'Set DXB_PRESET_GOPATH to an existing Go dependency cache.' >&2
  exit 2
fi
module_cache=$(cd -- "$module_cache" && pwd)
mkdir -p -- "$2"
evidence=$(cd -- "$2" && pwd)
if [[ -n $(find "$evidence" -mindepth 1 -maxdepth 1 -print -quit) ]]; then
  echo 'Refusing an existing evidence directory with contents.' >&2
  exit 2
fi
work=$(mktemp -d)
trap 'rm -rf -- "$work"' EXIT
docker_bin=${DXB_DOCKER:-docker}
docker_host=${DXB_DOCKER_HOST:-unix:///var/run/docker.sock}
go_image=${DXB_PRESET_GO_IMAGE:-golang:1.24.4-bookworm}
run_image=${DXB_PRESET_RUN_IMAGE:-debian:trixie-slim}
docker_call() {
  env -u DOCKER_HOST -u DOCKER_CONTEXT -u DOCKER_TLS -u DOCKER_TLS_VERIFY -u DOCKER_CERT_PATH \
    "$docker_bin" --host="$docker_host" "$@"
}
# Image acquisition is separate from this network-disabled verification.
docker_call image inspect "$go_image" "$run_image" >/dev/null
docker_call run --rm --pull=never --network none \
  -v "$root/station:/src:ro" -v "$module_cache:/go:ro" -v "$work:/out" -w /src \
  -e GOPATH=/go -e GOCACHE=/tmp/go-build -e GOPROXY=off -e GOTOOLCHAIN=local \
  -e CGO_ENABLED=0 -e GOMAXPROCS=4 -e GOFLAGS=-buildvcs=false \
  "$go_image" go test -c -tags=presetintegration -o /out/preset-http.test ./internal/cli \
  > "$evidence/compile.log" 2>&1
docker_call run --rm --pull=never --network none --read-only --cap-drop=ALL \
  --security-opt=no-new-privileges --pids-limit=128 --memory=256m \
  --tmpfs /tmp:rw,nosuid,nodev,noexec,size=32m,mode=1777 \
  --tmpfs /var/lib/dxberry/station:rw,nosuid,nodev,noexec,size=8m,mode=700 \
  -v "$work/preset-http.test:/fixture/preset-http.test:ro" \
  -v "$binary:/fixture/dxberry-preset:ro" -e GOMAXPROCS=2 -e DXB_PRESET_INTEGRATION=1 \
  "$run_image" /fixture/preset-http.test -test.v -test.timeout=30s -test.run '^TestPresetHTTPActualCLI$' \
  > "$evidence/http.log" 2>&1
helper_sha=$(sha256sum < "$binary")
helper_sha=${helper_sha%% *}
printf '{"version":1,"kind":"preset-http-cli-integration","result":"passed","binary_sha256":"%s","radio_control_tested":false,"physical_acceptance":false,"pi_installed":false}\n' \
  "$helper_sha" > "$evidence/result.json"
cat "$evidence/http.log"
