#!/usr/bin/env bash
# Exercise the actual native generation helper in hardware-free Docker.
set -euo pipefail
umask 077
if [[ $# != 2 || ! -f $1 || ! -x $1 || -L $1 ]]; then
  echo 'Usage: test-program-state-cli.sh NATIVE_HELPER EMPTY_EVIDENCE_DIRECTORY' >&2
  exit 2
fi
root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
binary=$(cd -- "$(dirname -- "$1")" && printf '%s/%s' "$PWD" "$(basename -- "$1")")
module_cache=${DXB_PROGRAMSTATE_GOPATH:-}
if [[ -z $module_cache || ! -d $module_cache ]]; then
  echo 'Set DXB_PROGRAMSTATE_GOPATH to an existing Go dependency cache.' >&2
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
# Source checkouts can be private to another UID. Make only this public unit
# fixture readable in the capability-free containers; preserve checkout modes.
install -m 444 "$root/provision/templates/dxberry-screen@.service" "$work/dxberry-screen@.service"
mkdir "$work/lib"
install -m 444 "$root/provision/lib/common.sh" "$work/lib/common.sh"
install -m 444 "$root/provision/lib/radio.sh" "$work/lib/radio.sh"
cat > "$work/uncertain-helper" <<'HELPER'
#!/bin/bash
/opt/dxberry/bin/dxberry-program-state "$@" >/dev/null || exit $?
exit 7
HELPER
chmod 555 "$work/uncertain-helper"
docker_bin=${DXB_DOCKER:-docker}
docker_host=${DXB_DOCKER_HOST:-unix:///var/run/docker.sock}
go_image=${DXB_PROGRAMSTATE_GO_IMAGE:-golang:1.24.4-bookworm}
run_image=${DXB_PROGRAMSTATE_RUN_IMAGE:-dxberry-program-state-runtime:fixture}
docker_call() {
  env -u DOCKER_HOST -u DOCKER_CONTEXT -u DOCKER_TLS -u DOCKER_TLS_VERIFY -u DOCKER_CERT_PATH \
    "$docker_bin" --host="$docker_host" "$@"
}
docker_call image inspect "$go_image" "$run_image" >/dev/null
docker_call run --rm --pull=never --network none \
  -v "$root/station:/src:ro" -v "$module_cache:/go:ro" -v "$work:/out" -w /src \
  -e GOPATH=/go -e GOCACHE=/tmp/go-build -e GOPROXY=off -e GOTOOLCHAIN=local \
  -e CGO_ENABLED=0 -e GOMAXPROCS=4 -e GOFLAGS=-buildvcs=false \
  "$go_image" go test -c -tags=programstateintegration -o /out/program-state.test ./cmd/dxberry-program-state \
  > "$evidence/compile.log" 2>&1
for fixture in root unprivileged activation record apply bootstrap; do
  user=0:0
  test_name=TestProgramStateActualCLI
  if [[ $fixture == unprivileged ]]; then user=65534:65534; test_name=TestProgramStateActualUnprivilegedCLI; fi
  if [[ $fixture == activation ]]; then test_name=TestProgramStateUnitActivationPrefix; fi
  if [[ $fixture == record ]]; then test_name=TestProgramStateActualRadioRecordWriter; fi
  if [[ $fixture == apply ]]; then test_name=TestProgramStateActualRadioApplyCoordinator; fi
  station_mount=(--tmpfs "/var/lib/dxberry/station:rw,nosuid,nodev,noexec,size=8m,mode=700")
  if [[ $fixture == bootstrap ]]; then test_name=TestProgramStateActualProvisionBootstrap; station_mount=(); fi
  docker_call run --rm --pull=never --network none --read-only --cap-drop=ALL --user="$user" \
    --security-opt=no-new-privileges --pids-limit=128 --memory=256m \
    --tmpfs /tmp:rw,nosuid,nodev,noexec,size=32m,mode=1777 \
    --tmpfs /var/lib/dxberry:rw,nosuid,nodev,noexec,size=16m,mode=755 \
    "${station_mount[@]}" \
    --tmpfs /run/dxberry:rw,nosuid,nodev,noexec,size=1m,mode=700 \
    -v "$work/program-state.test:/fixture/program-state.test:ro" \
    -v "$binary:/opt/dxberry/bin/dxberry-program-state:ro" \
    -v "$work/dxberry-screen@.service:/fixture/dxberry-screen@.service:ro" \
    -v "$work/lib/common.sh:/fixture/lib/common.sh:ro" \
    -v "$work/lib/radio.sh:/fixture/lib/radio.sh:ro" \
    -v "$work/uncertain-helper:/fixture/uncertain-helper:ro" \
    -e GOMAXPROCS=2 -e DXB_PROGRAMSTATE_INTEGRATION=1 \
    "$run_image" /fixture/program-state.test -test.v -test.timeout=45s -test.run="^$test_name$" \
    -program-state-binary=/opt/dxberry/bin/dxberry-program-state > "$evidence/$fixture.log" 2>&1
  grep -q "^--- PASS: $test_name " "$evidence/$fixture.log"
done
helper_sha=$(sha256sum < "$binary")
helper_sha=${helper_sha%% *}
printf '{"version":1,"kind":"program-generation-cli-integration","result":"passed","binary_sha256":"%s","unit_prefix_executed":true,"radio_record_writer_executed":true,"radio_apply_coordinator_executed":true,"runtime_state_writer_executed":true,"provision_bootstrap_executed":true,"systemd_manager_tested":false,"unit_tracking_tested":false,"radio_control_tested":false,"physical_acceptance":false,"pi_installed":false}\n' \
  "$helper_sha" > "$evidence/result.json"
cat "$evidence/root.log" "$evidence/unprivileged.log" "$evidence/activation.log" "$evidence/record.log" "$evidence/apply.log" "$evidence/bootstrap.log"
