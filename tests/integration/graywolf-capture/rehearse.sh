#!/usr/bin/env bash
# Disposable native ARM64 CI host only. Both the network and devices are private.
set -euo pipefail
export LC_ALL=C
[[ $(id -u) == 0 && $(uname -m) == aarch64 && ${GITHUB_ACTIONS:-} == true ]]
[[ $(systemctl show graywolf.service -p LoadState --value) == not-found ]]
[[ ! -e /var/lib/graywolf && ! -e /etc/asound.conf ]]
candidate=$(realpath "${1:?candidate executable}")
release=$(realpath "${2:?released deb}")
test "$("$candidate" --version)" = v0.14.14-4978244d
printf '%s  %s\n' 5cf6b193ac5083fb64e58de1cfc801b28eeaf848dd8a23783c9638b1548fe605 "$release" | sha256sum -c
work=$(mktemp -d)
trial=/run/systemd/system/graywolf.service.d/90-modem-trial.conf
dropins=${trial%/*}
mkdir -p "$dropins"
printf '[Service]\nPrivateNetwork=yes\nPrivateDevices=yes\n' > "$dropins/10-isolation.conf"
printf '%s\n' 'pcm.!default { type null }' 'pcm.dxb_fixture { type null hint { show on description "DXBerry synthetic PCM" } }' > /etc/asound.conf
apt-get install -y "$release"
test "$(dpkg-query -W -f='${Version} ${Architecture}' graywolf)" = '0.14.14 arm64'
sha() { sha256sum "$1" | cut -d' ' -f1; }
released_hash=$(sha /usr/bin/graywolf-modem)
candidate_hash=$(sha "$candidate")
test "$candidate_hash" != "$released_hash"
server_hash=$(sha /usr/bin/graywolf)
install -D -m 755 "$candidate" /opt/graywolf-validation/graywolf-modem
pid() { systemctl show graywolf.service -p MainPID --value; }
api() { nsenter -t "$(pid)" -n curl -fsS --max-time 2 -b "$work/cookies" -c "$work/cookies" "$@"; }
url=http://127.0.0.1:8080/api
ready() {
  for _ in {1..100}; do
    if api "$url/auth/setup" > /dev/null 2>&1; then return 0; fi
    sleep .2
  done
  systemctl status graywolf.service --no-pager || true
  return 1
}
post() { api -H 'Content-Type: application/json' --data-binary "$2" "$url/$1"; }
creds='{"username":"fixture","password":"isolated-fixture-password"}'
systemctl start graywolf.service
ready
post auth/setup "$creds" >/dev/null
post auth/login "$creds" >/dev/null
input=$(post audio-devices '{"name":"Fixture RX","direction":"input","source_type":"soundcard","device_path":"dxb_fixture","sample_rate":48000,"channels":1,"format":"s16le"}' | jq -er '.id')
channel=$(post channels "$(jq -cn --argjson id "$input" '{name:"Fixture RX",enabled:true,input_device_id:$id,modem_type:"afsk",bit_rate:1200}')" | jq -er '.id')
settings() {
  api "$url/audio-devices" | jq -Sc .
  api "$url/channels" | jq -Sc 'map(del(.backing))'
  python3 - <<'DB'
import json
import sqlite3
with sqlite3.connect("file:/var/lib/graywolf/graywolf.db?mode=ro", uri=True) as db:
    for table in ("audio_devices", "channels", "ptt_configs", "beacons", "digipeater_configs", "i_gate_configs"):
        print(json.dumps(db.execute("SELECT * FROM " + table + " ORDER BY id").fetchall()))
DB
}
settings > "$work/expected.json"
verify() {
  local label=$1 expected=$2 parent child level
  ready
  post auth/login "$creds" >/dev/null
  parent=$(pid)
  test "$(sha "/proc/$parent/exe")" = "$server_hash"
  test "$(readlink /proc/1/ns/net)" != "$(readlink "/proc/$parent/ns/net")"
  test ! -e "/proc/$parent/root/dev/snd"
  for _ in {1..100}; do
    child=$(pgrep -P "$parent" -x graywolf-modem | head -1 || :)
    if [[ -n $child && $(sha "/proc/$child/exe" 2>/dev/null) == "$expected" ]]; then
      if level=$(api "$url/status" | jq -er --argjson id "$channel" '.channels[] | select(.id==$id and .tx_frames==0 and .device_rms_dbfs < 0) | .device_rms_dbfs'); then
        test "$(pid)" = "$parent"
        test "$(sha /proc/"$child"/exe)" = "$expected"
        settings > "$work/current.json"
        diff -u "$work/expected.json" "$work/current.json"
        api "$url/channels" | jq -e --argjson id "$channel" 'any(.[]; .id==$id and .backing.modem.active and (.backing.tx.capable==false))' >/dev/null
        test "$(sha /usr/bin/graywolf-modem)" = "$released_hash"
        echo "$label verified: parent $parent, modem $child, input RMS $level dBFS, modem SHA256 $expected"
        return 0
      fi
    fi
    sleep .2
  done
  journalctl -u graywolf.service -n 35 --no-pager
  return 1
}
systemctl restart graywolf.service
verify released "$released_hash"
initial=$(systemctl show graywolf.service -p InvocationID --value)
cat > "$work/rollback" <<'ROLLBACK'
#!/bin/bash
set -eu
rm -f /run/systemd/system/graywolf.service.d/90-modem-trial.conf
systemctl daemon-reload
systemctl restart graywolf.service
ROLLBACK
chmod 700 "$work/rollback"
systemd-run --quiet --unit=graywolf-trial-rollback --on-active=50s --timer-property=AccuracySec=1s "$work/rollback"
systemctl is-active --quiet graywolf-trial-rollback.timer
printf '[Service]\nBindReadOnlyPaths=/opt/graywolf-validation/graywolf-modem:/usr/bin/graywolf-modem\n' > "$trial"
systemctl daemon-reload
systemctl restart graywolf.service
verify candidate "$candidate_hash"
active=$(systemctl show graywolf.service -p InvocationID --value)
test "$active" != "$initial"
for _ in {1..90}; do
  if [[ ! -e $trial && $(systemctl show graywolf.service -p InvocationID --value) != "$active" ]] && systemctl is-active --quiet graywolf.service; then break; fi
  sleep 1
done
test ! -e "$trial"
verify restored "$released_hash"
test "$(systemctl show graywolf-trial-rollback.service -p Result --value)" = success
echo 'Automatic rollback restored the released modem and persisted receiver configuration.'
