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
