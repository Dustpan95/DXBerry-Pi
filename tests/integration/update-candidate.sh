#!/bin/bash
# Start the actual unpublished bundle in disposable Docker mounts, without a
# manager or radio devices. This does not establish installation or RF acceptance.
set -euo pipefail
[[ ${DXB_UPDATE_CANDIDATE_FIXTURE:-} == 1 && $EUID == 0 ]]
[[ ! -e /dev/snd && ! -d /run/systemd/system ]]
for path in /opt/dxberry /var/lib/dxberry /run/dxberry /etc/dxberry /tmp; do
  [[ $(findmnt -n -o FSTYPE --mountpoint "$path") == tmpfs ]]
done
[[ -z $(ls -A /opt/dxberry) && -z $(ls -A /var/lib/dxberry) ]]
pid=''
stop_console() {
  [[ -n $pid ]] || return 0
  kill -TERM "$pid"
  for (( i=0; i<100; i++ )); do
    if ! kill -0 "$pid" 2>/dev/null; then
      wait "$pid"
      pid=''
      return 0
    fi
    sleep .1
  done
  kill -KILL "$pid" 2>/dev/null || true
  wait "$pid" || true
  pid=''
  echo 'Console did not stop within ten seconds.' >&2
  return 1
}
cleanup() {
  local rc=$?
  trap - EXIT
  stop_console || rc=1
  if (( rc )) && [[ -f /tmp/console.log ]]; then cat /tmp/console.log >&2; fi
  exit "$rc"
}
trap cleanup EXIT
tar -xzf /fixture/update.tar.gz --no-same-owner --strip-components=1 -C /opt/dxberry
for binary in dxberryd dxberry-preset dxberry-program-state dxberry-wsjtx-settings; do
  [[ -f /opt/dxberry/bin/$binary && ! -L /opt/dxberry/bin/$binary && -x /opt/dxberry/bin/$binary ]]
done
version=$(cat /opt/dxberry/VERSION)
[[ $(/opt/dxberry/bin/dxberryd -version) == "$version" ]]
[[ $(/opt/dxberry/bin/dxberryd -wsjtx-protocol) == "$(cat /opt/dxberry/share/wsjtx-native-protocol)" ]]
# Model the existing protected 0755 station parent and empty radio registry.
# The marker is fixture data, not a Graywolf configuration or hardware test.
mkdir -m 755 /var/lib/dxberry/station
printf '{"version":1,"radios":{},"gps":{"device":"none","baud":9600,"pps":""}}\n' > /var/lib/dxberry/radios.json
printf 'manual-channel-fixture: preserve\n' > /var/lib/dxberry/manual-channel-fixture
sha256sum /var/lib/dxberry/radios.json /var/lib/dxberry/manual-channel-fixture > /tmp/preserved.sha256
[[ $(/opt/dxberry/bin/dxberry-program-state prepare-advance) == '{"ok":true,"protocol":1}' ]]
[[ $(stat -c '%u:%a' /var/lib/dxberry/station) == 0:755 ]]
[[ $(stat -c '%u:%a' /var/lib/dxberry/station/program-state) == 0:700 ]]
sha256sum /var/lib/dxberry/station/program-state/epoch > /tmp/epoch.sha256
for (( start=0; start<2; start++ )); do
  /opt/dxberry/bin/dxberryd >> /tmp/console.log 2>&1 &
  pid=$!
  ready=0
  for (( i=0; i<100; i++ )); do
    if curl -fsS --max-time 1 http://127.0.0.1:8090/api/version > /tmp/version.json 2>/dev/null; then
      ready=1
      break
    fi
    kill -0 "$pid"
    sleep .1
  done
  (( ready ))
  jq -e --arg version "$version" '.version == $version' /tmp/version.json > /dev/null
  curl -fsS --max-time 2 http://127.0.0.1:8090/login > /tmp/login.html
  [[ -s /tmp/login.html ]]
  [[ ! -e /var/lib/dxberry/station/wsjtx && ! -e /run/dxberry/radio-events ]]
  sha256sum -c /tmp/epoch.sha256 /tmp/preserved.sha256
  stop_console
  [[ ! -e /run/dxberry/station-programs/wsjtx.sock ]]
done
sha256sum -c /tmp/epoch.sha256 /tmp/preserved.sha256
jq -cn --arg version "$version" '{kind:"update-candidate-startup",result:"passed",version:$version,starts:2,generation_bootstrap:true,existing_parent_mode_preserved:true,native_inventory_initialized:false,queue_activated:false,fixture_data_preserved:true,systemd_manager_tested:false,pi_installed:false,hardware_acceptance:false}'
