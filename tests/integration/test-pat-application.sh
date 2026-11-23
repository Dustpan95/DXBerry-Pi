#!/usr/bin/env bash
# Actual-package protocol check in hardware-free Docker; never installs on a station.
set -euo pipefail
if [[ $# != 2 || ! -f $1 || -L $1 ]]; then
  echo 'Usage: test-pat-application.sh VERIFIED_PACKAGE.deb EMPTY_EVIDENCE_DIRECTORY' >&2
  exit 2
fi
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
package=$(cd -- "$(dirname -- "$1")" && printf '%s/%s' "$PWD" "$(basename -- "$1")")
mkdir -p -- "$2"
evidence=$(cd -- "$2" && pwd)
if [[ -e $evidence/pat-application.json || -L $evidence/pat-application.json ]]; then
  echo 'Refusing to overwrite previous application evidence; choose an empty destination.' >&2
  exit 2
fi
work=$(mktemp -d)
trap 'rm -rf -- "$work"' EXIT
docker_bin=${DXB_DOCKER:-docker}
docker_host=${DXB_DOCKER_HOST:-unix:///var/run/docker.sock}
go_image=${DXB_PAT_GO_IMAGE:-golang:1.24.4-bookworm}
run_image=${DXB_PAT_RUN_IMAGE:-debian:trixie-slim}
docker_call() {
  env -u DOCKER_HOST -u DOCKER_CONTEXT -u DOCKER_TLS -u DOCKER_TLS_VERIFY -u DOCKER_CERT_PATH \
    "$docker_bin" --host="$docker_host" "$@"
}
# Images must be prepared separately; an acceptance run cannot silently fetch
# new tools/dependencies or gain external connectivity.
docker_call image inspect "$go_image" "$run_image" >/dev/null
docker_call run --rm --pull=never --network none \
  -v "$root/station:/src:ro" -v "$work:/out" -w /src \
  -e CGO_ENABLED=0 -e GOTOOLCHAIN=local -e GOPROXY=off -e GOMAXPROCS=4 \
  "$go_image" go test -c -tags=patcompat -o /out/pat-compat.test ./internal/pat
docker_call run --rm --pull=never --network none --read-only \
  --cap-drop=ALL --cap-add=SETUID --cap-add=SETGID --cap-add=CHOWN --cap-add=KILL --cap-add=DAC_OVERRIDE \
  --security-opt=no-new-privileges --pids-limit=128 --memory=512m \
  --tmpfs /tmp:rw,exec,nosuid,nodev,size=192m,mode=1777 \
  -v "$work/pat-compat.test:/test/pat-compat.test:ro" \
  -v "$package:/input/package.deb:ro" -v "$evidence:/evidence" \
  -e GOMAXPROCS=2 \
  -e DXB_PAT_COMPAT_PACKAGE=/input/package.deb -e DXB_PAT_COMPAT_REPORT=/evidence/pat-application.json \
  "$run_image" /test/pat-compat.test -test.v -test.timeout=60s -test.run '^TestPatApplicationCompatibility$'
