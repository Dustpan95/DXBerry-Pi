#!/usr/bin/env bash
# Run only in an isolated container without radio devices or external networking.
# GRAYWOLF_TEST_BIN points to an extracted release binary, never the live station.
set -euo pipefail
root=$(cd "$(dirname "$0")/../.." && pwd)
bin=${GRAYWOLF_TEST_BIN:?set GRAYWOLF_TEST_BIN to the isolated Graywolf binary}
work=$(mktemp -d)
pid=''
cleanup() {
  if [[ -n $pid ]]; then kill "$pid" 2>/dev/null || true; wait "$pid" 2>/dev/null || true; fi
  rm -rf "$work"
}
trap cleanup EXIT
"$bin" -config "$work/graywolf.db" -history-db "$work/history.db" \
  -tile-cache-dir "$work/tiles" -http 127.0.0.1:18080 -modem '' >"$work/server.log" 2>&1 &
pid=$!
for ((i=0; i<50; i++)); do
  curl -fsS http://127.0.0.1:18080/api/auth/setup >"$work/ready.json" 2>/dev/null && break
  sleep .1
done
jq -e '.needs_setup == true' "$work/ready.json" >/dev/null
# These are disposable fixture credentials, unrelated to any station login.
printf '%s' '{"username":"fixture","password":"offline-fixture-password"}' |
  curl -fsS -H 'Content-Type: application/json' --data-binary @- \
    http://127.0.0.1:18080/api/auth/setup >/dev/null

export DXB_LIB=$root/provision/lib DXB_STATE_DIR=$work/state DXB_LOG_FILE=$work/provision.log
export DXB_GW_API=http://127.0.0.1:18080/api DXB_GW_COOKIES=$work/cookies
export DXB_GW_SECRET_FILE=$work/graywolf.secret
mkdir -p "$DXB_STATE_DIR"
printf 'USER=fixture\nPASSWORD=offline-fixture-password\n' >"$DXB_GW_SECRET_FILE"
# shellcheck source=provision/lib/common.sh
source "$DXB_LIB/common.sh"
# shellcheck source=provision/lib/config.sh
source "$DXB_LIB/config.sh"
# shellcheck source=provision/lib/graywolf.sh
source "$DXB_LIB/graywolf.sh"
# shellcheck source=provision/lib/radio.sh
source "$DXB_LIB/radio.sh"
# shellcheck source=provision/lib/apps/graywolf.sh
source "$DXB_LIB/apps/graywolf.sh"
DXB_RADIOS='{"version":1,"radios":{"fixture":{"audio":{"path":"no-hardware"},"ptt":{"method":"none"}}}}'
systemctl() { printf '%s\n' "$*" >>"$work/systemctl.log"; }
api() { dxb_gw_login_any >/dev/null; dxb_gw_api "$@"; }
check() { jq -e "$1" >/dev/null || { echo "Graywolf contract check failed: $1" >&2; exit 1; }; }

# Unrelated, disabled RX-only channel: wiring must preserve user-managed records.
other=$(api POST /audio-devices '{"name":"unrelated","direction":"input","source_type":"soundcard","device_path":"unused","sample_rate":48000,"channels":1,"format":"s16le"}')
other_id=$(jq -r .id <<<"$other")
api POST /channels "$(jq -cn --argjson id "$other_id" '{name:"unrelated",input_device_id:$id,modem_type:"afsk",enabled:false}')" >/dev/null
other_before=$(api GET /channels | jq -Sc 'map(select(.name == "unrelated"))')

echo 'Checking first-time radio wiring against the released Graywolf API'
app_graywolf_wire fixture
devices=$(api GET /audio-devices)
rx=$(jq -r '.[] | select(.name == "fixture" and .direction == "input") | .id' <<<"$devices")
tx=$(jq -r '.[] | select(.name == "fixture TX" and .direction == "output") | .id' <<<"$devices")
[[ -n $rx && -n $tx && $rx != "$tx" ]]
channel=$(api GET /channels | jq -c '.[] | select(.name == "fixture")')
ch=$(jq -r .id <<<"$channel")
check ".input_device_id == $rx and .output_device_id == $tx and .modem_type == \"afsk\" and .bit_rate == 1200" <<<"$channel"
check '.method == "none" and (.persist|type) == "number"' <<<"$(api GET "/ptt/$ch")"

# Rewiring preserves decoder settings, disabled state, gain (including 0 dB output),
# and numeric CSMA tuning; zero is a valid persistence value too.
api PUT "/audio-devices/$rx/gain" '{"gain_db":-9}' >/dev/null
api PUT "/audio-devices/$tx/gain" '{"gain_db":0}' >/dev/null
api PUT "/channels/$ch" "$(jq '.enabled=false | .profile="B" | .num_slicers=5 | del(.id,.backing,.ptt)' <<<"$channel")" >/dev/null
ptt=$(api GET "/ptt/$ch")
api PUT "/ptt/$ch" "$(jq '.persist=0 | .slot_time_ms=37 | .dwait_ms=91 | del(.id)' <<<"$ptt")" >/dev/null
echo 'Checking repeat wiring and preservation of operator settings'
app_graywolf_wire fixture
check 'length == 3' <<<"$(api GET /audio-devices)"
check '.gain_db == -9' <<<"$(api GET "/audio-devices/$rx")"
check '.gain_db == 0' <<<"$(api GET "/audio-devices/$tx")"
check '.enabled == false and .profile == "B" and .num_slicers == 5' <<<"$(api GET "/channels/$ch")"
check '.persist == 0 and .slot_time_ms == 37 and .dwait_ms == 91' <<<"$(api GET "/ptt/$ch")"
[[ $(api GET /channels | jq -Sc 'map(select(.name == "unrelated"))') == "$other_before" ]]

echo 'Checking removal of both owned audio directions'
app_graywolf_unwire fixture
check 'length == 1 and .[0].name == "unrelated"' <<<"$(api GET /audio-devices)"
[[ $(api GET /channels | jq -Sc 'map(select(.name == "unrelated"))') == "$other_before" ]]
app_graywolf_unwire fixture
echo 'Graywolf API integration checks passed'
