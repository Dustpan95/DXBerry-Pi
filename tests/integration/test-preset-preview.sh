#!/usr/bin/env bash
# Run root-owned preset preview fixtures without station state or hardware.
set -euo pipefail
umask 077
if [[ $# != 1 ]]; then
  echo 'Usage: test-preset-preview.sh EMPTY_EVIDENCE_DIRECTORY' >&2
  exit 2
fi
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
module_cache=${DXB_PRESET_GOPATH:-}
if [[ -z $module_cache || ! -d $module_cache ]]; then
  echo 'Set DXB_PRESET_GOPATH to an existing Go dependency cache.' >&2
  exit 2
fi
module_cache=$(cd -- "$module_cache" && pwd)
mkdir -p -- "$1"
evidence=$(cd -- "$1" && pwd)
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
docker_call image inspect "$go_image" "$run_image" >/dev/null
docker_call run --rm --pull=never --network none \
  -v "$root/station:/src:ro" -v "$module_cache:/go:ro" -v "$work:/out" -w /src \
  -e GOPATH=/go -e GOCACHE=/tmp/go-build -e GOPROXY=off -e GOTOOLCHAIN=local \
  -e CGO_ENABLED=0 -e GOMAXPROCS=4 -e GOFLAGS=-buildvcs=false \
  "$go_image" go test -c -o /out/preset-preview.test ./internal/presetpreview \
  > "$evidence/compile.log" 2>&1
docker_call run --rm --pull=never --network none \
  -v "$root/station:/src:ro" -v "$module_cache:/go:ro" -v "$work:/out" -w /src \
  -e GOPATH=/go -e GOCACHE=/tmp/go-build -e GOPROXY=off -e GOTOOLCHAIN=local \
  -e CGO_ENABLED=0 -e GOMAXPROCS=4 -e GOFLAGS=-buildvcs=false \
  "$go_image" go test -c -o /out/preset-preview-http.test ./internal/httpapi \
  > "$evidence/http-compile.log" 2>&1
docker_call run --rm --pull=never --network none --read-only --user 0:0 --cap-drop=ALL \
  --security-opt=no-new-privileges --pids-limit=128 --memory=256m \
  --tmpfs /tmp:rw,nosuid,nodev,noexec,size=32m,mode=1777 \
  -v "$work/preset-preview.test:/fixture/preset-preview.test:ro" \
  -e GOMAXPROCS=2 -e DXB_PRESET_PREVIEW_INTEGRATION=1 \
  "$run_image" /fixture/preset-preview.test -test.v -test.timeout=30s \
  > "$evidence/preview.log" 2>&1
docker_call run --rm --pull=never --network none --read-only --user 0:0 --cap-drop=ALL \
  --security-opt=no-new-privileges --pids-limit=128 --memory=256m \
  --tmpfs /tmp:rw,nosuid,nodev,noexec,size=32m,mode=1777 \
  -v "$work/preset-preview-http.test:/fixture/preset-preview-http.test:ro" \
  -e GOMAXPROCS=2 -e DXB_PRESET_PREVIEW_INTEGRATION=1 \
  "$run_image" /fixture/preset-preview-http.test -test.v -test.timeout=30s -test.run '^TestPresetPreview' \
  > "$evidence/http.log" 2>&1
binary_sha=$(sha256sum < "$work/preset-preview.test")
binary_sha=${binary_sha%% *}
http_sha=$(sha256sum < "$work/preset-preview-http.test")
http_sha=${http_sha%% *}
printf '{"version":1,"kind":"preset-preview-integration","result":"passed","test_binary_sha256":"%s","http_test_binary_sha256":"%s","effective_target_verifier":"synthetic","physical_acceptance":false,"pi_installed":false}\n' \
  "$binary_sha" "$http_sha" > "$evidence/result.json"
cat "$evidence/preview.log"
cat "$evidence/http.log"
