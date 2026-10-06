#!/bin/bash
# shellcheck shell=bash
# Status collectors behind dxberry-status (console spec section 7). Each dxb_status_<part> prints one
# JSON object, or fails with its reason as the last line on stderr; dxb_status_json runs the parts in
# parallel and wraps each as {"ok":true,...} or {"ok":false,"error":"..."}, so one source that cannot
# be read never hides the others. Expects common.sh and graywolf.sh to be sourced.

: "${DXB_PROC:=/proc}"
: "${DXB_SYSFS_ROOT:=/sys}"
: "${DXB_DT_MODEL:=$DXB_PROC/device-tree/model}"
: "${DXB_DF_PATH:=/}"
: "${DXB_VCGENCMD:=vcgencmd}"
: "${DXB_CHRONYC:=chronyc}"
: "${DXB_IW:=iw}"
: "${DXB_BIN:=/opt/dxberry/bin}"
: "${DXB_NETWATCH:=$DXB_BIN/dxberry-netwatch}"
: "${DXB_RADIO_CMD:=$DXB_BIN/dxberry-radio}"
: "${DXB_RELEASE_FILE:=/etc/dxberry-release}"
: "${DXB_RUN_DIR:=/run/dxberry}"
: "${DXB_RIGCTLD_RUN_DIR:=$DXB_RUN_DIR/rigctld}"
: "${DXB_STATUS_GW_COOKIES:=$DXB_RUN_DIR/status-graywolf.cookies}"
: "${DXB_STATUS_CURL:=_dxb_status_curl}"

DXB_STATUS_PARTS='pi network graywolf radios time release services'

# ---- pi ------------------------------------------------------------------------------------
# _dxb_status_throttle HEX: vcgencmd get_throttled's bits by name (0-3 now, 16-19 since boot).
_dxb_status_throttle() {
  local raw=$1 v n b=()
  [[ $raw =~ ^0x[0-9a-fA-F]+$ ]] || return 1
  v=$(( raw ))
  for n in 0 1 2 3 16 17 18 19; do
    if (( (v >> n) & 1 )); then b+=(true); else b+=(false); fi
  done
  jq -cn --arg raw "$raw" --argjson b "[$(IFS=,; echo "${b[*]}")]" \
    '{raw: $raw, under_voltage_now: $b[0], freq_capped_now: $b[1], throttled_now: $b[2], soft_temp_limit_now: $b[3],
      under_voltage_since_boot: $b[4], freq_capped_since_boot: $b[5], throttled_since_boot: $b[6], soft_temp_limit_since_boot: $b[7]}'
}

dxb_status_pi() {
  local model='' temp=null throttle=null raw t l1=null l2=null l3=null mt=null mu=null dt=null du=null up=null
  [[ -r $DXB_DT_MODEL ]] && model=$(tr -d '\0' < "$DXB_DT_MODEL")
  if [[ -r $DXB_SYSFS_ROOT/class/thermal/thermal_zone0/temp ]] && read -r t < "$DXB_SYSFS_ROOT/class/thermal/thermal_zone0/temp" && [[ $t =~ ^-?[0-9]+$ ]]; then
    temp=$(awk -v t="$t" 'BEGIN { printf "%.1f", t / 1000 }')
  fi
  if raw=$("$DXB_VCGENCMD" get_throttled 2> /dev/null) && raw=$(_dxb_status_throttle "${raw#throttled=}"); then throttle=$raw; fi
  [[ -r $DXB_PROC/loadavg ]] && read -r l1 l2 l3 _ < "$DXB_PROC/loadavg"
  if [[ -r $DXB_PROC/meminfo ]]; then
    read -r mt mu < <(awk '/^MemTotal:/ { t = $2 } /^MemAvailable:/ { a = $2 }
      END { if (t) printf "%d %d\n", t * 1024, (t - a) * 1024; else print "null null" }' "$DXB_PROC/meminfo")
  fi
  read -r dt du < <(df -PB1 "$DXB_DF_PATH" 2> /dev/null | awk 'NR == 2 { print $2, $3; ok = 1 } END { if (!ok) print "null null" }')
  if [[ -r $DXB_PROC/uptime ]]; then read -r up _ < "$DXB_PROC/uptime"; up=${up%%.*}; fi
  [[ $up =~ ^[0-9]+$ ]] || up=null
  jq -cn --arg model "$model" --argjson temp "$temp" --argjson throttle "$throttle" --argjson load "[$l1,$l2,$l3]" \
    --argjson mt "$mt" --argjson mu "$mu" --argjson dt "$dt" --argjson du "$du" --argjson up "$up" \
    '{model: $model, temp_c: $temp, throttle: $throttle, load: $load, mem_total: $mt, mem_used: $mu,
      disk_total: $dt, disk_used: $du, uptime_s: $up}'
}

# ---- network -------------------------------------------------------------------------------
dxb_status_network() {
  local st holder='' line='' addr='' prefix=null gw wifi=null sig ssid
  st=$("$DXB_NETWATCH" --status 2> /dev/null) || st=''
  [[ $st =~ \(([a-z0-9]+)\ holds ]] && holder=${BASH_REMATCH[1]}
  st=${st%% *}
  [[ $st =~ ^(ETH|WIFI|NONE)$ ]] || st=unknown
  if [[ -n $holder ]]; then
    line=$(ip -o -4 addr show dev "$holder" 2> /dev/null | awk '{ print $4; exit }')
    addr=${line%/*}
    [[ $line == */* ]] && prefix=${line#*/}
    [[ $prefix =~ ^[0-9]+$ ]] || prefix=null
  fi
  gw=$(ip -4 route show default 2> /dev/null | awk '{ for (i = 1; i < NF; i++) if ($i == "via") { print $(i + 1); exit } }')
  if [[ $holder == wlan0 ]]; then
    # signal first: an SSID may contain spaces, so it takes the rest of the line
    read -r sig ssid < <("$DXB_IW" dev wlan0 link 2> /dev/null | awk '
      /^[[:space:]]*SSID: / { s = $0; sub(/^[[:space:]]*SSID: /, "", s) }
      /^[[:space:]]*signal: / { g = $2 }
      END { printf "%s %s\n", (g == "" ? "null" : g), s }')
    [[ $sig =~ ^-?[0-9]+$ ]] || sig=null
    wifi=$(jq -cn --arg s "${ssid:-}" --argjson g "$sig" '{ssid: $s, signal_dbm: $g}')
  fi
  jq -cn --arg st "$st" --arg i "$holder" --arg a "$addr" --argjson p "$prefix" --arg gw "$gw" --argjson w "$wifi" \
    --arg h "$(hostname 2> /dev/null)" \
    '{state: $st, interface: $i, address: $a, prefix: $p, gateway: $gw, wifi: $w, hostname: $h}'
}

# ---- time ----------------------------------------------------------------------------------
# chronyc -c tracking: field 1 reference id, 2 reference, 3 stratum, 5 system time offset (s),
# 14 leap status. The selected source in -c sources carries "*"; "#" marks a reference clock,
# which chrony-dxberry.conf names GPS or PPS.
dxb_status_time() {
  local tr refid ref stratum sys leap src synced=false off=null
  tr=$("$DXB_CHRONYC" -c tracking 2> /dev/null) || { echo "chrony is not answering" >&2; return 1; }
  IFS=, read -r refid ref stratum _ sys _ _ _ _ _ _ _ _ leap <<< "$tr"
  [[ -n $leap && $leap != 'Not synchronised' && $refid != 00000000 ]] && synced=true
  src=$("$DXB_CHRONYC" -c sources 2> /dev/null | awk -F, '$2 == "*" { print ($1 == "#" ? $3 : "NTP"); found = 1; exit } END { if (!found) print "none" }')
  [[ $stratum =~ ^[0-9]+$ ]] || stratum=null
  [[ $sys =~ ^-?[0-9]*\.?[0-9]+$ ]] && off=$(awk -v o="$sys" 'BEGIN { printf "%.3f", o * 1000 }')
  jq -cn --argjson s "$synced" --arg src "$src" --arg ref "$ref" --argjson st "$stratum" --argjson off "$off" \
    '{synced: $s, source: $src, reference: $ref, stratum: $st, offset_ms: $off}'
}

# ---- release -------------------------------------------------------------------------------
_dxb_status_pkg_version() {
  local st='' v=''
  read -r st v < <(dpkg-query -W -f '${db:Status-Status} ${Version}\n' "$1" 2> /dev/null)
  [[ $st == installed ]] && printf '%s' "$v"
  return 0
}

# update stays null until the update check (console spec section 11.1) exists
dxb_status_release() {
  local v='' c=''
  if [[ -r $DXB_RELEASE_FILE ]]; then
    v=$(sed -n 's/^DXBERRY_VERSION=//p' "$DXB_RELEASE_FILE" | head -1)
    c=$(sed -n 's/^DXBERRY_COMMIT=//p' "$DXB_RELEASE_FILE" | head -1)
  fi
  jq -cn --arg v "$v" --arg c "$c" --arg g "$(dxb_gw_installed_version)" --arg k "$(_dxb_status_pkg_version cockpit-ws)" \
    '{dxberry: $v, dxberry_commit: $c, graywolf: $g, cockpit: $k, update: null}'
}

# ---- services ------------------------------------------------------------------------------
# The units of console spec section 9.1: one rigctld per radio DXBerry runs (its env file).
dxb_status_unit_list() {
  local f n
  echo graywolf.service
  for f in "$DXB_RIGCTLD_RUN_DIR"/*.env; do
    [[ -f $f ]] || continue
    n=${f##*/}
    echo "rigctld@${n%.env}.service"
  done
  printf '%s\n' gpsd.service chrony.service dxberry-netwatch.service dxberry-radio-hotplug.service dxberry-radio-wire.service cockpit.socket
}

dxb_status_services() {
  local u props out='[]'
  while IFS= read -r u; do
    props=$(systemctl show -p LoadState,ActiveState,SubState,Result,Type,UnitFileState "$u" 2> /dev/null) || props=''
    out=$(jq -c --arg u "$u" --arg p "$props" '. + [
      ($p | split("\n") | map(select(test("=")) | capture("^(?<key>[^=]+)=(?<value>.*)$")) | from_entries) as $m
      | {unit: $u, load: ($m.LoadState // "unknown"), active: ($m.ActiveState // "unknown"), sub: ($m.SubState // ""),
         result: ($m.Result // ""), type: ($m.Type // ""), enabled: ($m.UnitFileState // "")}]' <<< "$out")
  done < <(dxb_status_unit_list)
  jq -cn --argjson u "$out" '{units: $u}'
}

# ---- graywolf ------------------------------------------------------------------------------
_dxb_status_curl() { curl --connect-timeout 2 --max-time 5 "$@"; }

# _dxb_status_gw_login: a session from the stored admin secret, into the status cookie jar. The
# password goes to jq through the environment and to curl on stdin, never into an argument.
_dxb_status_gw_login() {
  local u pw rc
  [[ -r $DXB_GW_SECRET_FILE ]] || { echo "no stored Graywolf login ($DXB_GW_SECRET_FILE)" >&2; return 1; }
  u=$(sed -n 's/^USER=//p' "$DXB_GW_SECRET_FILE" | head -1)
  pw=$(sed -n 's/^PASSWORD=//p' "$DXB_GW_SECRET_FILE" | head -1)
  [[ -n $u && -n $pw ]] || { echo "the stored Graywolf login is incomplete ($DXB_GW_SECRET_FILE)" >&2; return 1; }
  ( umask 077; : > "$DXB_GW_COOKIES" )
  dxb_gw_api POST /auth/login "$(PW=$pw jq -cn --arg u "$u" '{username: $u, password: env.PW}')" > /dev/null 2>&1
  rc=$?
  case $rc in
    0) return 0 ;;
    22) echo "Graywolf refused the stored login" >&2 ;;
    *) echo "Graywolf's API did not answer (curl exit $rc)" >&2 ;;
  esac
  return 1
}

# _dxb_status_gw_get PATH: GET through the kept session; logs in once when it is missing or expired.
_dxb_status_gw_get() {
  local out
  if out=$(dxb_gw_api GET "$1" 2> /dev/null); then printf '%s\n' "$out"; return 0; fi
  _dxb_status_gw_login || return 1
  dxb_gw_api GET "$1"
}

# _dxb_status_db_bytes PATH: the history database with its WAL and shared-memory files, or null.
_dxb_status_db_bytes() {
  local f total=0 any=0
  [[ -n $1 ]] || { echo null; return 0; }
  for f in "$1" "$1-wal" "$1-shm"; do
    [[ -f $f ]] || continue
    total=$(( total + $(stat -c %s "$f") )); any=1
  done
  if (( any )); then echo "$total"; else echo null; fi
}

dxb_status_graywolf() {
  # shellcheck disable=SC2034  # both are read by dxb_gw_api through bash's dynamic scope
  local DXB_GW_COOKIES=$DXB_STATUS_GW_COOKIES DXB_CURL=$DXB_STATUS_CURL
  local props active sub result hp port api_ok=false api_err='' ch='[]' list id c stats p ig=null pl=null errf
  props=$(systemctl show -p ActiveState,SubState,Result graywolf.service 2> /dev/null)
  active=$(sed -n 's/^ActiveState=//p' <<< "$props")
  sub=$(sed -n 's/^SubState=//p' <<< "$props")
  result=$(sed -n 's/^Result=//p' <<< "$props")
  hp=${DXB_GW_API#*://}; hp=${hp%%/*}; port=${hp##*:}
  [[ $port =~ ^[0-9]+$ ]] || port=8080
  if [[ $active != active ]]; then
    api_err='graywolf is not running'
  else
    mkdir -p "$(dirname "$DXB_GW_COOKIES")" 2> /dev/null
    [[ -f $DXB_GW_COOKIES ]] || ( umask 077; : > "$DXB_GW_COOKIES" )
    errf=$(mktemp)
    if list=$(_dxb_status_gw_get /channels 2> "$errf") && jq -e 'type == "array"' <<< "$list" > /dev/null 2>&1; then
      api_ok=true
      for id in $(jq -r '.[] | .id | numbers' <<< "$list"); do
        if ! stats=$(_dxb_status_gw_get "/channels/$id/stats" 2> /dev/null) || ! jq -e 'type == "object"' <<< "$stats" > /dev/null 2>&1; then stats='{}'; fi
        c=$(jq -c --argjson id "$id" 'map(select(.id == $id)) | .[0]' <<< "$list")
        ch=$(jq -c --argjson c "$c" --argjson s "$stats" '. + [{id: $c.id, name: ($c.name // ""), enabled: ($c.enabled // true),
          rx_frames: $s.rx_frames, tx_frames: $s.tx_frames, rx_bad_fcs: $s.rx_bad_fcs}]' <<< "$ch")
      done
      if p=$(_dxb_status_gw_get /igate 2> /dev/null) && jq -e 'type == "object"' <<< "$p" > /dev/null 2>&1; then
        ig=$(jq -c '{connected: (.connected // false), server: (.server // ""), rf_to_is_gated, is_to_rf_gated}' <<< "$p")
      fi
      if p=$(_dxb_status_gw_get /position-log 2> /dev/null) && jq -e 'type == "object"' <<< "$p" > /dev/null 2>&1; then
        pl=$(jq -c --argjson b "$(_dxb_status_db_bytes "$(jq -r '.db_path // ""' <<< "$p")")" \
          '{enabled: (.enabled // false), path: (.db_path // ""), bytes: $b}' <<< "$p")
      fi
    else
      api_err=$(grep -v '^[[:space:]]*$' "$errf" | tail -1)
      [[ -n $api_err ]] || api_err="Graywolf's API did not answer"
    fi
    rm -f "$errf"
  fi
  jq -cn --arg a "$active" --arg s "$sub" --arg r "$result" --arg v "$(dxb_gw_installed_version)" --argjson port "$port" \
    --argjson ok "$api_ok" --arg e "$api_err" --argjson ig "$ig" --argjson ch "$ch" --argjson pl "$pl" \
    '{active: $a, sub: $s, result: $r, version: $v, web_port: $port, api_ok: $ok, api_error: $e,
      igate: $ig, channels: $ch, position_log: $pl}'
}

# ---- radios --------------------------------------------------------------------------------
# Exactly what dxberry-radio status --json reports (console spec section 7.1).
dxb_status_radios() {
  local out rc
  out=$("$DXB_RADIO_CMD" status --json 2> /dev/null); rc=$?
  (( rc == 0 )) || { echo "dxberry-radio status failed (exit $rc)" >&2; return 1; }
  jq -ce 'select(type == "object" and has("radios"))' <<< "$out" 2> /dev/null \
    || { echo "dxberry-radio status returned something unexpected" >&2; return 1; }
}

# ---- the report ----------------------------------------------------------------------------
# _dxb_status_part NAME: dxb_status_NAME's object with ok:true, or {ok:false, error} carrying the
# last line it wrote to stderr.
_dxb_status_part() {
  local name=$1 out errf e
  errf=$(mktemp)
  if out=$("dxb_status_$name" 2> "$errf") && jq -e 'type == "object"' <<< "$out" > /dev/null 2>&1; then
    jq -c '{ok: true} + .' <<< "$out"
  else
    e=$(grep -v '^[[:space:]]*$' "$errf" | tail -1)
    jq -cn --arg e "${e:-$name could not be read}" '{ok: false, error: $e}'
  fi
  rm -f "$errf"
}

# dxb_status_json [PART...]: the report (every part when none is named), parts read in parallel.
dxb_status_json() {
  local parts=("$@") p dir out
  (( ${#parts[@]} )) || read -ra parts <<< "$DXB_STATUS_PARTS"
  dir=$(mktemp -d) || { echo "could not create a temporary directory" >&2; return 1; }
  for p in "${parts[@]}"; do _dxb_status_part "$p" > "$dir/$p" & done
  wait
  out=$(jq -cn --arg g "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" '{generated: $g}')
  for p in "${parts[@]}"; do
    out=$(jq -c --arg p "$p" --slurpfile v "$dir/$p" '.[$p] = $v[0]' <<< "$out")
  done
  rm -rf "$dir"
  printf '%s\n' "$out"
}

# dxb_status_text: the report (stdin, JSON) as one readable line per part, temperatures in °F first.
dxb_status_text() {
  jq -r '
    def n1: if type == "number" then ((. * 10 | floor) / 10 | tostring) else "?" end;
    def temp($c): if ($c | type) == "number" then ((($c * 9 / 5 + 32) + 0.5 | floor | tostring) + "°F (" + ($c | n1) + "°C)") else "?" end;
    def mb: if type == "number" then ((. / 1048576 | floor | tostring) + " MB") else "?" end;
    def gb: if type == "number" then (((. / 1073741824 * 10 | floor) / 10 | tostring) + " GB") else "?" end;
    def up: if type == "number" then ((. / 86400 | floor | tostring) + " d " + ((. % 86400) / 3600 | floor | tostring) + " h") else "?" end;
    def line($n; fmt): if has($n) then (if .[$n].ok then $n + ": " + (.[$n] | fmt) else $n + ": unavailable (" + (.[$n].error // "unknown error") + ")" end) else empty end;
    def power: if . == null then "" else
      ([(if .under_voltage_now then "UNDER-VOLTAGE NOW" else empty end),
        (if .throttled_now then "THROTTLED NOW" else empty end),
        (if (.under_voltage_now | not) and .under_voltage_since_boot then "under-voltage since boot" else empty end),
        (if (.throttled_now | not) and .throttled_since_boot then "throttled since boot" else empty end)]
       | if length > 0 then "; " + join(", ") else "" end) end;
    line("pi"; .model + ", " + temp(.temp_c) + ", load " + ((.load[0] // "?") | tostring) + ", RAM " + (.mem_used | mb) + " of " + (.mem_total | mb)
      + ", disk " + (.disk_used | gb) + " of " + (.disk_total | gb) + ", up " + (.uptime_s | up) + (.throttle | power)),
    line("network"; .state + (if .interface != "" then " on " + .interface + " " + .address + "/" + (.prefix | tostring) else "" end)
      + (if .gateway != "" then " via " + .gateway else "" end)
      + (if .wifi != null then ", WiFi \"" + .wifi.ssid + "\" " + (.wifi.signal_dbm | tostring) + " dBm" else "" end)),
    line("graywolf"; (if .active == "active" then "running" else .active end) + ", version " + (if .version == "" then "not installed" else .version end)
      + (if .api_ok then
           (if .igate != null then ", iGate " + (if .igate.connected then "connected to " + .igate.server else "not connected" end) else "" end)
           + ([.channels[] | ", channel " + .name + " rx " + (.rx_frames | tostring) + " tx " + (.tx_frames | tostring) + " bad FCS " + (.rx_bad_fcs | tostring)] | join(""))
           + (if .position_log != null then ", position log " + (if .position_log.enabled then "on" else "off" end) else "" end)
         else " (" + .api_error + ")" end)),
    line("radios"; (.radios | length | tostring) + " radio(s)"
      + ([.radios | to_entries[] | ", " + .key + " " + (if .value.present then "present" else "unplugged" end) + (if .value.owner != "" then " (" + .value.owner + ")" else "" end)] | join(""))),
    line("time"; if .synced then "synced to " + .source + (if .source == "NTP" and .reference != "" then " " + .reference else "" end) + ", offset " + (.offset_ms | tostring) + " ms" else "not synced" end),
    line("release"; "DXBerry " + .dxberry + ", Graywolf " + (if .graywolf == "" then "-" else .graywolf end) + ", Cockpit " + (if .cockpit == "" then "-" else .cockpit end)),
    (if has("services") then
       (if .services.ok then "services:", (.services.units[] | "  " + .unit + ": " + (if .load == "not-found" then "not installed" else .active + " (" + .sub + ")" end))
        else "services: unavailable (" + (.services.error // "unknown error") + ")" end)
     else empty end)'
}
