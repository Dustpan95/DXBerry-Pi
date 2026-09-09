#!/bin/bash
# shellcheck shell=bash
# Graywolf: install the latest release (checksum-verified) and seed it through its REST API.

: "${DXB_GW_API:=http://127.0.0.1:8080/api}"
: "${DXB_GW_RELEASES:=https://github.com/chrissnell/graywolf/releases}"
: "${DXB_GW_COOKIES:=/run/dxberry-graywolf.cookies}"
: "${DXB_GW_SEED_STATE:=$DXB_STATE_DIR/graywolf-seed.env}"
: "${DXB_CURL:=curl}"
: "${DXB_TTY:=/dev/tty}"

dxb_gw_release_base() {
  local v=${DXB_CFG[GRAYWOLF_VERSION]:-}
  if [[ -n $v ]]; then printf '%s/download/%s' "$DXB_GW_RELEASES" "$v"; else printf '%s/latest/download' "$DXB_GW_RELEASES"; fi
}

# stdin: checksums.txt; $1: dpkg architecture -> "sha256 filename" of the matching .deb
dxb_gw_pick_deb() { awk -v a="$1" '$2 ~ ("^graywolf_[0-9.]+_" a "\\.deb$") { print $1, $2; exit }'; }

dxb_gw_installed_version() { dpkg-query -W -f '${Version}' graywolf 2> /dev/null || true; }

dxb_gw_fetch() {
  local i
  for i in 1 2 3; do
    "$DXB_CURL" -fsSL --connect-timeout 15 --max-time 300 "$1" && return 0
    sleep $(( i * 5 ))
  done
  return 1
}

# dxb_gw_fetch_to FILE URL: lets curl own the output file (and its own retry), so a partial
# attempt followed by a retry can never concatenate bytes into FILE, and a stalled transfer
# cannot hang forever.
dxb_gw_fetch_to() {
  "$DXB_CURL" -fsSL --connect-timeout 15 --max-time 300 --retry 2 --retry-delay 5 -o "$1" "$2"
}

# graywolf's .deb declares no Depends (measured: `dpkg -s graywolf` has no Depends line), but
# graywolf-modem - a child process it spawns, not a separate unit - needs libasound.so.2 and
# crash-loops every ~30s without it; graywolf.service itself stays active regardless. The
# package is libasound2t64 on Trixie (candidate 1.2.14-1+rpt1), libasound2 on older Debian.
# Idempotent and silent when the library is already present, so a plain re-run heals an
# existing box. graywolf.service works without it either way, so a failure here is reported,
# never fatal to the rest of dxb_gw_install.
dxb_gw_install_runtime_deps() {
  if ldconfig -p 2> /dev/null | grep -q 'libasound\.so\.2'; then return 0; fi
  if DEBIAN_FRONTEND=noninteractive apt-get install -y libasound2t64 > /dev/null 2>&1 \
    || DEBIAN_FRONTEND=noninteractive apt-get install -y libasound2 > /dev/null 2>&1; then
    dxb_info "installed ALSA runtime for graywolf-modem"
    return 0
  fi
  dxb_step_failed graywolf "could not install the ALSA runtime (libasound2t64) that graywolf-modem needs"
  return 1
}

dxb_gw_install() {
  local base arch sums line sha name tmp v
  # Runs first, before any network access, so an offline already-provisioned box still gets its
  # crash-looping graywolf-modem healed by a plain dxberry-provision even when the release fetch
  # below fails.
  dxb_gw_install_runtime_deps
  base=$(dxb_gw_release_base)
  arch=${DXB_DPKG_ARCH:-$(dpkg --print-architecture)}
  sums=$(dxb_gw_fetch "$base/checksums.txt") || { dxb_step_failed graywolf "could not download checksums.txt from $base"; return 1; }
  line=$(printf '%s\n' "$sums" | dxb_gw_pick_deb "$arch")
  [[ -n $line ]] || { dxb_step_failed graywolf "release has no .deb for architecture $arch"; return 1; }
  sha=${line%% *}; name=${line#* }
  v=$(sed -E 's/^graywolf_([0-9.]+)_.*/\1/' <<< "$name")
  if [[ $(dxb_gw_installed_version) == "$v" ]]; then dxb_info "graywolf $v already installed"; return 0; fi
  tmp=$(mktemp -d)
  if ! dxb_gw_fetch_to "$tmp/$name" "$base/$name"; then dxb_step_failed graywolf "download of $name failed"; rm -rf "$tmp"; return 1; fi
  if [[ $(sha256sum "$tmp/$name" | cut -d' ' -f1) != "$sha" ]]; then dxb_step_failed graywolf "checksum mismatch for $name"; rm -rf "$tmp"; return 1; fi
  if ! DEBIAN_FRONTEND=noninteractive apt-get install -y "$tmp/$name" > /dev/null 2>&1; then dxb_step_failed graywolf "apt-get install of $name failed"; rm -rf "$tmp"; return 1; fi
  rm -rf "$tmp"
  dxb_info "installed graywolf $v"
  return 0
}

# ---- API client ----------------------------------------------------------------------------
dxb_gw_api() {
  local m=$1 p=$2 data=${3:-}
  local args=(-fsS -X "$m" -b "$DXB_GW_COOKIES" -c "$DXB_GW_COOKIES" -H 'Content-Type: application/json' "$DXB_GW_API$p")
  if [[ -n $data ]]; then
    # Body (may carry WEBUI_PASSWORD) goes via stdin, never as a literal argument: a literal
    # --data value would sit in this process's argv, visible to any other user via ps.
    args+=(--data-binary @-)
    printf '%s' "$data" | "$DXB_CURL" "${args[@]}"
  else
    "$DXB_CURL" "${args[@]}"
  fi
}
dxb_gw_wait_ready() { local i; for i in $(seq 1 60); do "$DXB_CURL" -fsS "$DXB_GW_API/auth/setup" > /dev/null 2>&1 && return 0; sleep 2; done; return 1; }
# 0 = setup needed, 1 = already set up, 2 = could not tell (request failed or body wasn't JSON
# with a needs_setup field) - the caller must treat 2 as a failure, never as "already configured".
dxb_gw_needs_setup() {
  local body ns
  body=$(dxb_gw_api GET /auth/setup) || return 2
  ns=$(printf '%s' "$body" | jq -r 'if has("needs_setup") then (.needs_setup | tostring) else empty end' 2> /dev/null)
  case $ns in
    true) return 0 ;;
    false) return 1 ;;
    *) return 2 ;;
  esac
}

_dxb_bool() { if [[ $1 == on ]]; then echo true; else echo false; fi; }
dxb_gw_payload_station() { jq -cn --arg c "${DXB_CFG[CALLSIGN]}" '{callsign: $c}'; }
dxb_gw_payload_igate() {
  jq -cn --arg s "${DXB_CFG[IGATE_SERVER]}" --argjson r "$(_dxb_bool "${DXB_CFG[IGATE_RF_TO_IS]}")" --argjson t "$(_dxb_bool "${DXB_CFG[IGATE_IS_TO_RF]}")" \
    '{enabled: true, server: $s, port: 14580, gate_rf_to_is: $r, gate_is_to_rf: $t}'
}
# dxb_gw_payload_beacon SEND_PATH [CHANNEL_ID]
dxb_gw_payload_beacon() {
  local sp=$1 ch=${2:-}
  jq -cn --arg lat "${DXB_CFG[LATITUDE]}" --arg lon "${DXB_CFG[LONGITUDE]}" --arg c "${DXB_CFG[BEACON_COMMENT]}" \
    --argjson i "${DXB_CFG[_INTERVAL_S]}" --arg sp "$sp" --arg p "${DXB_CFG[BEACON_PATH]}" \
    --arg st "${DXB_CFG[_SYMBOL_TABLE]}" --arg sy "${DXB_CFG[_SYMBOL]}" --arg ch "$ch" \
    '{type: "position", latitude: ($lat | tonumber), longitude: ($lon | tonumber), comment: $c, interval: $i, send_path: $sp, path: $p, symbol_table: $st, symbol: $sy, enabled: true} + (if $ch == "" then {} else {channel: ($ch | tonumber)} end)'
}
dxb_gw_payload_digi() { jq -cn --arg c "${DXB_CFG[CALLSIGN]}" '{enabled: true, my_call: $c, dedupe_window_seconds: 30}'; }
dxb_gw_payload_gps() { jq -cn '{enabled: true, source_type: "gpsd", gpsd_host: "localhost", gpsd_port: 2947}'; }
# dxb_gw_payload_rule CHANNEL ALIAS TYPE MAX_HOPS PRIORITY
dxb_gw_payload_rule() {
  jq -cn --argjson ch "$1" --arg a "$2" --arg t "$3" --argjson h "$4" --argjson p "$5" \
    '{from_channel: $ch, to_channel: $ch, alias: $a, alias_type: $t, max_hops: $h, priority: $p, action: "repeat", enabled: true}'
}

dxb_gw_seed_state_get() { sed -n "s/^$1=//p" "$DXB_GW_SEED_STATE" 2> /dev/null | head -1; }
# Writes the key atomically and reports dxb_set_kv's failure, not chmod's.
dxb_gw_seed_state_set() {
  local rc
  ( umask 077; [[ -f $DXB_GW_SEED_STATE ]] || : > "$DXB_GW_SEED_STATE" )
  dxb_set_kv "$DXB_GW_SEED_STATE" "$1" "$2"
  rc=$?
  chmod 600 "$DXB_GW_SEED_STATE" 2> /dev/null
  return $rc
}
dxb_gw_first_channel() { dxb_gw_api GET /channels 2> /dev/null | jq -r 'if type == "array" and length > 0 then .[0].id else "" end'; }

dxb_gw_login() {
  local pw=${DXB_CFG[WEBUI_PASSWORD]}
  if [[ $pw == "$DXB_APPLIED" ]]; then
    # No 2> /dev/null here: bash writes a read -p prompt to stderr, so discarding stderr would
    # turn the documented --reseed recovery into a silent 120 s hang.
    if ! read -rst 120 -p "Graywolf password for ${DXB_CFG[WEBUI_USER]}: " pw < "$DXB_TTY" || [[ -z $pw ]]; then
      echo >&2
      dxb_step_failed graywolf "WEBUI_PASSWORD was scrubbed and no terminal is available to prompt; set it in dxberry.txt and re-run"
      return 1
    fi
    echo >&2
  fi
  dxb_gw_api POST /auth/login "$(PW=$pw jq -cn --arg u "${DXB_CFG[WEBUI_USER]}" '{username: $u, password: env.PW}')" > /dev/null \
    || { dxb_step_failed graywolf "login as ${DXB_CFG[WEBUI_USER]} failed"; return 1; }
  dxb_secret_consumed WEBUI_PASSWORD
}

dxb_gw_seed_igate() {
  local cur merged
  cur=$(dxb_gw_api GET /igate/config 2> /dev/null) || cur='{}'
  jq -e 'type == "object"' <<< "$cur" > /dev/null 2>&1 || cur='{}'
  # id is Graywolf's only read-only field on this endpoint (measured against 0.14.13: PUT
  # rejects it with 400 "unknown field id"); the merge otherwise keeps operator-set fields alive
  # across a re-seed.
  merged=$(jq -c --argjson ours "$(dxb_gw_payload_igate)" '. + $ours | del(.id)' <<< "$cur")
  dxb_gw_api PUT /igate/config "$merged" > /dev/null || dxb_step_failed graywolf "iGate config update failed"
}

dxb_gw_seed_beacon() {
  local sp=${DXB_CFG[_SEND_PATH]} ch='' id cur merged
  if [[ $sp != is_only ]]; then
    ch=$(dxb_gw_first_channel)
    if [[ -z $ch ]]; then
      sp=is_only
      dxb_status_add "beacon: created as APRS-IS only until a radio channel exists (BEACON_SEND=${DXB_CFG[BEACON_SEND]} needs one)"
    fi
  fi
  id=$(dxb_gw_seed_state_get BEACON_ID)
  if [[ -n $id ]] && cur=$(dxb_gw_api GET "/beacons/$id" 2> /dev/null) && jq -e '.id' <<< "$cur" > /dev/null 2>&1; then
    # Same read-only-id convention as the iGate endpoint (G1) - not yet exercised on hardware
    # (no beacon configured on the test Pi), but it is the same API.
    merged=$(jq -c --argjson ours "$(dxb_gw_payload_beacon "$sp" "$ch")" '. + $ours | del(.id)' <<< "$cur")
    dxb_gw_api PUT "/beacons/$id" "$merged" > /dev/null || dxb_step_failed graywolf "beacon $id update failed"
  else
    id=$(dxb_gw_api POST /beacons "$(dxb_gw_payload_beacon "$sp" "$ch")" | jq -r '.id // empty')
    if [[ -n $id ]]; then
      dxb_gw_seed_state_set BEACON_ID "$id" || dxb_step_failed graywolf "beacon $id created but could not be saved to $DXB_GW_SEED_STATE"
    else
      dxb_step_failed graywolf "beacon creation failed"
    fi
  fi
}

# Seeds Graywolf's position source from gpsd when a GPS is configured. Once per box; --reseed repeats it.
dxb_gw_seed_gps() {
  (( DXB_CFG[_GPS] )) || return 0
  dxb_gw_api PUT /gps "$(dxb_gw_payload_gps)" > /dev/null || { dxb_step_failed graywolf "GPS source update failed"; return 1; }
  dxb_status_add "graywolf: position from gpsd (GPS_DEVICE=${DXB_CFG[GPS_DEVICE]})"
}

dxb_gw_seed_digi() {
  local ch hops
  dxb_gw_api PUT /digipeater "$(dxb_gw_payload_digi)" > /dev/null || dxb_step_failed graywolf "digipeater config update failed"
  [[ $(dxb_gw_seed_state_get RULES_SEEDED) == 1 ]] && return 0
  ch=$(dxb_gw_first_channel)
  if [[ -z $ch ]]; then
    dxb_status_add "digipeater: enabled; digipeater rules pending until a radio channel exists - run 'sudo dxberry-provision --reseed' after adding one, or pick the preset in the Digipeater page"
    return 0
  fi
  if [[ $(dxb_gw_api GET /digipeater/rules 2> /dev/null | jq 'if type == "array" then length else 0 end') != 0 ]]; then
    dxb_info "digipeater rules already exist; not adding preset rules"
    return 0
  fi
  if [[ ${DXB_CFG[DIGIPEATER]} == wide ]]; then hops=2; else hops=1; fi
  dxb_gw_api POST /digipeater/rules "$(dxb_gw_payload_rule "$ch" "${DXB_CFG[CALLSIGN]}" exact 1 1)" > /dev/null || dxb_step_failed graywolf "digipeater rule creation failed"
  dxb_gw_api POST /digipeater/rules "$(dxb_gw_payload_rule "$ch" WIDE widen "$hops" 10)" > /dev/null || dxb_step_failed graywolf "digipeater rule creation failed"
  dxb_gw_seed_state_set RULES_SEEDED 1 || dxb_step_failed graywolf "digipeater rules created but could not be saved to $DXB_GW_SEED_STATE"
  dxb_status_add "digipeater: ${DXB_CFG[DIGIPEATER]} preset rules created on channel $ch"
}

# dxb_gw_seed RESEED(0|1)
dxb_gw_seed() {
  local reseed=${1:-0} setup_rc
  dxb_gw_wait_ready || { dxb_step_failed graywolf "API at $DXB_GW_API not reachable"; return 1; }
  rm -f "$DXB_GW_COOKIES"; ( umask 077; : > "$DXB_GW_COOKIES" )
  dxb_gw_needs_setup
  setup_rc=$?
  if (( setup_rc == 2 )); then
    dxb_step_failed graywolf "could not query /auth/setup (request failed or returned an unexpected body)"
    return 1
  elif (( setup_rc == 0 )); then
    [[ ${DXB_CFG[WEBUI_PASSWORD]} != "$DXB_APPLIED" ]] || { dxb_step_failed graywolf "admin password already scrubbed; set WEBUI_PASSWORD in dxberry.txt and re-run"; return 1; }
    dxb_gw_api POST /auth/setup "$(PW=${DXB_CFG[WEBUI_PASSWORD]} jq -cn --arg u "${DXB_CFG[WEBUI_USER]}" '{username: $u, password: env.PW}')" > /dev/null \
      || { dxb_step_failed graywolf "creating admin ${DXB_CFG[WEBUI_USER]} failed"; return 1; }
    dxb_info "graywolf admin '${DXB_CFG[WEBUI_USER]}' created"
    dxb_secret_consumed WEBUI_PASSWORD
  elif (( ! reseed )); then
    dxb_info "graywolf already set up; not reseeding (use --reseed)"
    dxb_status_add "graywolf: already configured (not reseeded)"
    return 0
  fi
  dxb_gw_login || return 1
  dxb_gw_seed_gps
  if [[ -n ${DXB_CFG[CALLSIGN]:-} ]]; then
    dxb_gw_api PUT /station/config "$(dxb_gw_payload_station)" > /dev/null || dxb_step_failed graywolf "station callsign update failed"
    dxb_gw_seed_igate
    (( DXB_CFG[_BEACON] )) && dxb_gw_seed_beacon
    [[ ${DXB_CFG[DIGIPEATER]} == off ]] || dxb_gw_seed_digi
    dxb_status_add "graywolf: seeded station ${DXB_CFG[CALLSIGN]}, iGate ${DXB_CFG[IGATE_SERVER]}"
  else
    dxb_status_add "graywolf: installed, no CALLSIGN in dxberry.txt - finish station setup in the web UI"
  fi
  dxb_gw_api POST /auth/logout > /dev/null 2>&1 || true
  rm -f "$DXB_GW_COOKIES"
  return 0
}
