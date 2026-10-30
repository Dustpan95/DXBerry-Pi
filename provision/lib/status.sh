#!/bin/bash
# shellcheck shell=bash
# Status collectors behind dxberry-status (console spec section 7). Each dxb_status_<part> prints one
# JSON object, or fails with its reason as the last line on stderr; dxb_status_json runs the parts in
# parallel and wraps each as {"ok":true,...} or {"ok":false,"error":"..."}, so one source that cannot
# be read never hides the others. Expects common.sh, config.sh, graywolf.sh, gps.sh and apps.sh to be
# sourced (config, gps and apps for the station part). While a browser listens the station runs this
# every 30 s, so the collectors stay in this shell where they can: every subshell, pipeline and jq is
# a fork on a Pi (tests/test_status.sh holds a run to a limit).

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
: "${DXB_APP_CMD:=$DXB_BIN/dxberry-app}"
: "${DXB_STATION_CMD:=$DXB_BIN/dxberryd}"
: "${DXB_LOGBOOK_CMD:=$DXB_BIN/dxberry-logbook}"

DXB_STATUS_PARTS='pi network graywolf radios time release services station apps'

# ---- pi ------------------------------------------------------------------------------------
# _dxb_status_throttle HEX VAR: vcgencmd get_throttled's bits by name (0-3 now, 16-19 since boot),
# as JSON into VAR.
_dxb_status_throttle() {
  local raw=$1 v n b=()
  [[ $raw =~ ^0x[0-9a-fA-F]+$ ]] || return 1
  v=$(( raw ))
  for n in 0 1 2 3 16 17 18 19; do
    if (( (v >> n) & 1 )); then b+=(true); else b+=(false); fi
  done
  printf -v "$2" '{"raw":"%s","under_voltage_now":%s,"freq_capped_now":%s,"throttled_now":%s,"soft_temp_limit_now":%s,"under_voltage_since_boot":%s,"freq_capped_since_boot":%s,"throttled_since_boot":%s,"soft_temp_limit_since_boot":%s}' "$raw" "${b[@]}"
}

dxb_status_pi() {
  local model='' temp=null throttle=null raw t k n total=0 avail=0 l1=null l2=null l3=null mt=null mu=null dt='' du='' up=null
  [[ -r $DXB_DT_MODEL ]] && IFS= read -r -d '' model < "$DXB_DT_MODEL"
  if [[ -r $DXB_SYSFS_ROOT/class/thermal/thermal_zone0/temp ]] && read -r t < "$DXB_SYSFS_ROOT/class/thermal/thermal_zone0/temp" && [[ $t =~ ^-?[0-9]+$ ]]; then
    temp=$(awk -v t="$t" 'BEGIN { printf "%.1f", t / 1000 }')
  fi
  raw=$("$DXB_VCGENCMD" get_throttled 2> /dev/null) && _dxb_status_throttle "${raw#throttled=}" throttle
  [[ -r $DXB_PROC/loadavg ]] && read -r l1 l2 l3 _ < "$DXB_PROC/loadavg"
  if [[ -r $DXB_PROC/meminfo ]]; then
    while read -r k n _; do
      case $k in MemTotal:) total=$n ;; MemAvailable:) avail=$n ;; esac
    done < "$DXB_PROC/meminfo"
    if (( total )); then mt=$(( total * 1024 )) mu=$(( (total - avail) * 1024 )); fi
  fi
  { read -r _; read -r _ dt du _; } < <(df -PB1 "$DXB_DF_PATH" 2> /dev/null)
  if [[ -r $DXB_PROC/uptime ]]; then read -r up _ < "$DXB_PROC/uptime"; up=${up%%.*}; fi
  [[ $up =~ ^[0-9]+$ ]] || up=null
  jq -cn --arg model "$model" --argjson temp "$temp" --argjson throttle "$throttle" --argjson load "[$l1,$l2,$l3]" \
    --argjson mt "$mt" --argjson mu "$mu" --argjson dt "${dt:-null}" --argjson du "${du:-null}" --argjson up "$up" \
    '{model: $model, temp_c: $temp, throttle: $throttle, load: $load, mem_total: $mt, mem_used: $mu,
      disk_total: $dt, disk_used: $du, uptime_s: $up}'
}

# ---- network -------------------------------------------------------------------------------
dxb_status_network() {
  local st holder='' line='' addr='' prefix=null gw='' sig=null ssid='' h='' l
  st=$("$DXB_NETWATCH" --status 2> /dev/null) || st=''
  [[ $st =~ \(([a-z0-9]+)\ holds ]] && holder=${BASH_REMATCH[1]}
  st=${st%% *}
  [[ $st =~ ^(ETH|WIFI|NONE)$ ]] || st=unknown
  if [[ -n $holder ]]; then
    read -r _ _ _ line _ < <(ip -o -4 addr show dev "$holder" 2> /dev/null)
    addr=${line%/*}
    [[ $line == */* ]] && prefix=${line#*/}
    [[ $prefix =~ ^[0-9]+$ ]] || prefix=null
  fi
  while read -r l; do
    if [[ " $l" =~ [[:space:]]via[[:space:]]+([^[:space:]]+) ]]; then gw=${BASH_REMATCH[1]}; break; fi
  done < <(ip -4 route show default 2> /dev/null)
  if [[ $holder == wlan0 ]]; then
    while IFS= read -r l; do
      if [[ $l =~ ^[[:space:]]*SSID:\ (.*)$ ]]; then ssid=${BASH_REMATCH[1]}
      elif [[ $l =~ ^[[:space:]]*signal:\ +([^[:space:]]*) ]]; then sig=${BASH_REMATCH[1]}
      fi
    done < <("$DXB_IW" dev wlan0 link 2> /dev/null)
    [[ $sig =~ ^-?[0-9]+$ ]] || sig=null
    read -r ssid <<< "$ssid" # spaces at either end are not the name
  fi
  [[ -r $DXB_PROC/sys/kernel/hostname ]] && read -r h < "$DXB_PROC/sys/kernel/hostname"
  jq -cn --arg st "$st" --arg i "$holder" --arg a "$addr" --argjson p "$prefix" --arg gw "$gw" --arg s "$ssid" --argjson g "$sig" --arg h "$h" \
    '{state: $st, interface: $i, address: $a, prefix: $p, gateway: $gw,
      wifi: (if $i == "wlan0" then {ssid: $s, signal_dbm: $g} else null end), hostname: $h}'
}

# ---- time ----------------------------------------------------------------------------------
# chronyc -c tracking: field 1 reference id, 2 reference, 3 stratum, 5 system time offset (s),
# 14 leap status. The selected source in -c sources carries "*"; "#" marks a reference clock,
# which chrony-dxberry.conf names GPS or PPS.
dxb_status_time() {
  local tr refid ref stratum sys leap src=none synced=false off=null mark sel name
  tr=$("$DXB_CHRONYC" -c tracking 2> /dev/null) || { echo "chrony is not answering" >&2; return 1; }
  IFS=, read -r refid ref stratum _ sys _ _ _ _ _ _ _ _ leap <<< "$tr"
  [[ -n $leap && $leap != 'Not synchronised' && $refid != 00000000 ]] && synced=true
  while IFS=, read -r mark sel name _; do
    [[ $sel == '*' ]] || continue
    if [[ $mark == '#' ]]; then src=$name; else src=NTP; fi
    break
  done < <("$DXB_CHRONYC" -c sources 2> /dev/null)
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

# update: the cached update check (console spec section 11.1) in brief, or null before the first
# check (and for a cache that does not parse). Each part is null when the check could not read it
# (its .error), never false/0: a failed check must not read as "up to date". Reading the cache
# never runs a check: that is the Updates card's job.
dxb_status_release() {
  local v='' c='' s='' cache=${DXB_UPDATE_CACHE:-$DXB_STATE_DIR/update-check.json} u=null l
  if [[ -r $DXB_RELEASE_FILE ]]; then
    while IFS= read -r l || [[ -n $l ]]; do
      case $l in
        DXBERRY_VERSION=*) [[ -n $v ]] || v=${l#*=} ;;
        DXBERRY_COMMIT=*) [[ -n $c ]] || c=${l#*=} ;;
      esac
    done < "$DXB_RELEASE_FILE"
  fi
  if [[ -r $cache ]]; then
    u=$(jq -c '{graywolf: (if .graywolf.error then null else .graywolf.update == true end),
      dxberry: (if .dxberry.error then null else .dxberry.update == true end),
      system: (if .system.error then null else .system.count // 0 end), checked_at: (.checked_at // null)}' "$cache" 2> /dev/null) || u=null
  fi
  [[ -x $DXB_STATION_CMD ]] && IFS= read -r s < <("$DXB_STATION_CMD" -version 2> /dev/null)
  jq -cn --arg v "$v" --arg c "$c" --arg g "$(dxb_gw_installed_version)" --arg k "$s" --argjson u "${u:-null}" \
    '{dxberry: $v, dxberry_commit: $c, graywolf: $g, dxberryd: $k, update: $u}'
}

# ---- services ------------------------------------------------------------------------------
# The units of console spec section 9.1: one rigctld per radio DXBerry runs (its env file).
dxb_status_unit_list() {
  local f n u m=''
  echo graywolf.service
  for f in "$DXB_RIGCTLD_RUN_DIR"/*.env; do
    [[ -f $f ]] || continue
    n=${f##*/}
    echo "rigctld@${n%.env}.service"
  done
  printf '%s\n' gpsd.service chrony.service dxberry-netwatch.service dxberry-radio-hotplug.service dxberry-radio-wire.service dxberryd.service nginx.service
  # the programs: every screen session systemd knows (running or not), the logging bridge and its
  # flush timer, and the web units only where their unit files exist (Wavelog's php-fpm and database; nginx is always listed above)
  while read -r u _; do
    if [[ $u =~ ^dxberry-screen@[A-Za-z0-9_.:-]+\.service$ ]]; then echo "$u"; fi
  done < <(systemctl list-units --all --plain --no-legend 'dxberry-screen@*' 2> /dev/null)
  printf '%s\n' dxberry-logbridge.service dxberry-logbook-flush.timer
  while read -r u _; do
    case $u in
      '') ;;
      mariadb.service) m=$u ;;
      *) echo "$u" ;;
    esac
  done < <(systemctl list-unit-files --no-legend 'php*-fpm.service' mariadb.service 2> /dev/null)
  [[ -z $m ]] || echo "$m"
}

# One systemctl for every unit: it answers a block of properties per unit, in the order asked, with
# a blank line between them, and a block even for a unit that does not exist.
dxb_status_services() {
  local units props
  mapfile -t units < <(dxb_status_unit_list)
  props=$(systemctl show -p LoadState,ActiveState,SubState,Result,Type,UnitFileState "${units[@]}" 2> /dev/null)
  jq -cRs '(split("\n\n") | map(split("\n") | map(select(test("=")) | capture("^(?<key>[^=]+)=(?<value>.*)$")) | from_entries)) as $blocks
    | {units: [$ARGS.positional | to_entries[] | .key as $i | .value as $u | ($blocks[$i] // {}) as $m
        | {unit: $u, load: ($m.LoadState // "unknown"), active: ($m.ActiveState // "unknown"), sub: ($m.SubState // ""),
           result: ($m.Result // ""), type: ($m.Type // ""), enabled: ($m.UnitFileState // "")}]}' --args -- "${units[@]}" <<< "$props"
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

# _dxb_status_gw_get PATH VAR: GET through the kept session, the answer into VAR; logs in and
# retries only when the failure is an authentication failure - curl -f reports an HTTP error as
# exit 22, with "... error: 401" (a missing/expired session) or "... error: 403" on stderr. Any
# other failure (a 404, a timeout, ...) is returned as is, so a channel or endpoint that keeps
# failing never costs a fresh login (a database write) on every refresh. What went wrong is left
# in the caller's $errf.
_dxb_status_gw_get() {
  local out rc
  out=$(dxb_gw_api GET "$1" 2> "$errf")
  rc=$?
  if (( rc == 22 )) && grep -qE 'error: (401|403)\b' "$errf"; then
    _dxb_status_gw_login 2> "$errf" || return 1
    out=$(dxb_gw_api GET "$1" 2> "$errf")
    rc=$?
  fi
  printf -v "$2" '%s' "$out"
  return "$rc"
}

# _dxb_status_db_bytes PATH VAR: the size of the history database with its WAL and shared-memory
# files into VAR, or null.
_dxb_status_db_bytes() {
  local f n total=0 files=()
  printf -v "$2" null
  [[ -n $1 ]] || return 0
  for f in "$1" "$1-wal" "$1-shm"; do
    if [[ -f $f ]]; then files+=("$f"); fi
  done
  (( ${#files[@]} )) || return 0
  while read -r n; do total=$(( total + n )); done < <(stat -c %s "${files[@]}")
  printf -v "$2" '%s' "$total"
}

dxb_status_graywolf() {
  # shellcheck disable=SC2034  # both are read by dxb_gw_api through bash's dynamic scope
  local DXB_GW_COOKIES=$DXB_STATUS_GW_COOKIES DXB_CURL=$DXB_STATUS_CURL
  local l active='' sub='' result='' hp port api_ok=false api_err='' ch='[]' list ids id body stats=() ig='' pl='' path bytes=null errf
  while IFS= read -r l; do
    case $l in
      ActiveState=*) active=${l#*=} ;;
      SubState=*) sub=${l#*=} ;;
      Result=*) result=${l#*=} ;;
    esac
  done < <(systemctl show -p ActiveState,SubState,Result graywolf.service 2> /dev/null)
  hp=${DXB_GW_API#*://}; hp=${hp%%/*}; port=${hp##*:}
  [[ $port =~ ^[0-9]+$ ]] || port=8080
  if [[ $active != active ]]; then
    api_err='graywolf is not running'
  else
    [[ -d ${DXB_GW_COOKIES%/*} ]] || mkdir -p "${DXB_GW_COOKIES%/*}" 2> /dev/null
    [[ -f $DXB_GW_COOKIES ]] || ( umask 077; : > "$DXB_GW_COOKIES" )
    errf=$(mktemp)
    # "ok" first, so the channel ids come with proof that the answer was a list
    if _dxb_status_gw_get /channels list && ids=$(jq -r 'if type == "array" then "ok", (.[] | .id? | numbers) else empty end' <<< "$list" 2> /dev/null) && [[ $ids == ok* ]]; then
      api_ok=true
      ch=$list
      for id in ${ids#ok}; do
        _dxb_status_gw_get "/channels/$id/stats" body || body=''
        stats+=("$body")
      done
      _dxb_status_gw_get /igate ig || ig=''
      if _dxb_status_gw_get /position-log pl; then
        path=$(jq -r 'if type == "object" then .db_path // "" else empty end' <<< "$pl" 2> /dev/null)
        _dxb_status_db_bytes "$path" bytes
      else
        pl=''
      fi
    else
      api_err=$(grep -v '^[[:space:]]*$' "$errf" | tail -1)
      [[ -n $api_err ]] || api_err="Graywolf's API did not answer"
    fi
    rm -f "$errf"
  fi
  # the answers are read here, in one pass: one that is not a JSON object counts as no answer
  jq -cn --arg a "$active" --arg s "$sub" --arg r "$result" --arg v "$(dxb_gw_installed_version)" --argjson port "$port" \
    --argjson ok "$api_ok" --arg e "$api_err" --arg ch "$ch" --arg ig "$ig" --arg pl "$pl" --argjson b "$bytes" '
    def object: (try fromjson catch null) | if type == "object" then . else null end;
    ($ig | object) as $i | ($pl | object) as $l | [$ch | fromjson | .[] | select(.id? | numbers)] as $c
    | {active: $a, sub: $s, result: $r, version: $v, web_port: $port, api_ok: $ok, api_error: $e,
       igate: (if $i then $i | {connected: (.connected // false), server: (.server // ""), rf_to_is_gated, is_to_rf_gated} else null end),
       channels: [range($c | length) as $n | $c[$n] as $x | (($ARGS.positional[$n] // "") | object // {}) as $y
         | {id: $x.id, name: ($x.name // ""), enabled: ($x.enabled // true), rx_frames: $y.rx_frames, tx_frames: $y.tx_frames, rx_bad_fcs: $y.rx_bad_fcs}],
       position_log: (if $l then {enabled: ($l.enabled // false), path: ($l.db_path // ""), bytes: $b} else null end)}' --args -- "${stats[@]}"
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

# ---- station and programs (the DXBerry page's dashboard and Programs section) ---------------
# The callsign (without its -SSID) and the grid, exactly as the programs are told them
# (dxb_apps_station: LATITUDE and LONGITUDE from dxberry.txt, else the GPS fix, else "").
dxb_status_station() {
  local f st
  f="$(dxb_boot_dir)/dxberry.txt"
  if [[ -r $f ]]; then dxb_config_load "$f" > /dev/null 2>&1; dxb_config_validate > /dev/null 2>&1; fi
  st=$(dxb_apps_station) || { echo "the station could not be read" >&2; return 1; }
  jq -c '{callsign: .call, grid: .grid}' <<< "$st"
}

# _dxb_status_piece WHAT CMD ARGS...: CMD's JSON answer, run under a timeout
# (DXB_STATUS_APPS_TIMEOUT seconds, 20 by default; killed 5 s after that), or {"error": why} - its
# last line on stderr without the log timestamp, or what went wrong in words (WHAT names the piece:
# "the program list"; the page shows this text, so it names no command). Runs in the background of
# dxb_status_apps, inside its $dir.
_dxb_status_piece() {
  local what=$1 t=${DXB_STATUS_APPS_TIMEOUT:-20} out errf=$dir/$BASHPID.err rc e
  shift
  out=$(timeout -k 5 "$t" "$@" 2> "$errf"); rc=$?
  (( rc == 0 )) && jq -ce 'select(type == "object" or type == "array")' <<< "$out" 2> /dev/null && return 0
  if (( rc == 124 || rc == 137 )); then e="$what did not answer within $t seconds"
  elif (( rc == 0 )); then e="$what could not be read"
  else
    e=$(grep -v '^[[:space:]]*$' "$errf" | tail -1 | sed -E 's/^[0-9]{4}-[0-9]{2}-[0-9]{2} [0-9:]{8} \[[A-Z]+\] //')
    [[ -n $e ]] || e="$what could not be read (exit $rc)"
  fi
  jq -cn --arg e "$e" '{error: $e}'
}

# The programs (dxberry-app list), the logbook setting (dxberry-logbook get) and its 10 newest
# contacts (dxberry-logbook recent), read side by side. A piece that fails is {"error"} in its
# place; the part itself never fails, so the page still shows the other two.
dxb_status_apps() {
  local dir
  dir=$(mktemp -d) || { echo "could not create a temporary directory" >&2; return 1; }
  _dxb_status_piece "the program list" "$DXB_APP_CMD" list --json > "$dir/programs" &
  _dxb_status_piece "the logbook" "$DXB_LOGBOOK_CMD" get --json > "$dir/logbook" &
  _dxb_status_piece "the recent contacts" "$DXB_LOGBOOK_CMD" recent --json 10 > "$dir/recent" &
  wait
  jq -cn --slurpfile p "$dir/programs" --slurpfile l "$dir/logbook" --slurpfile r "$dir/recent" \
    '{programs: $p[0], logbook: $l[0], recent: $r[0]}'
  rm -rf "$dir"
}

# ---- the report ----------------------------------------------------------------------------
# dxb_status_json [PART...]: the report (every part when none is named), parts read in parallel.
# Each part's answer and stderr land in files, and one jq pass makes the report: an answer that is
# a JSON object gets ok:true, anything else (a failed collector leaves nothing) is {ok:false, error}
# carrying the last line the part wrote to stderr.
dxb_status_json() {
  local parts=("$@") p dir files=()
  (( ${#parts[@]} )) || read -ra parts <<< "$DXB_STATUS_PARTS"
  dir=$(mktemp -d) || { echo "could not create a temporary directory" >&2; return 1; }
  for p in "${parts[@]}"; do
    { "dxb_status_$p" > "$dir/$p" 2> "$dir/$p.err" || : > "$dir/$p"; } &
    files+=(--rawfile "$p" "$dir/$p" --rawfile "${p}_err" "$dir/$p.err")
  done
  wait
  # stamped last and to the millisecond: the DXBerry page sets its clocks from it
  jq -cn --arg g "$(date -u '+%Y-%m-%dT%H:%M:%S.%3NZ')" "${files[@]}" 'reduce $ARGS.positional[] as $p ({generated: $g};
    .[$p] = ($ARGS.named[$p] | try fromjson catch null
      | if type == "object" then {ok: true} + .
        else {ok: false, error: ([$ARGS.named[$p + "_err"] | splits("\n") | select(test("\\S"))] | last // ($p + " could not be read"))} end))' --args -- "${parts[@]}"
  rm -rf "$dir"
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
    line("pi"; .model + ", " + temp(.temp_c) + ", load " + (((.load // [])[0] // "?") | tostring) + ", RAM " + (.mem_used | mb) + " of " + (.mem_total | mb)
      + ", disk " + (.disk_used | gb) + " of " + (.disk_total | gb) + ", up " + (.uptime_s | up) + (.throttle | power)),
    line("network"; .state + (if .interface != "" then " on " + .interface + " " + .address + "/" + (.prefix | tostring) else "" end)
      + (if .gateway != "" then " via " + .gateway else "" end)
      + (if .wifi != null then ", WiFi \"" + .wifi.ssid + "\" " + (.wifi.signal_dbm | tostring) + " dBm" else "" end)),
    line("graywolf"; (if .active == "active" then "running" else .active end) + ", version " + (if .version == "" then "not installed" else .version end)
      + (if .api_ok then
           (if .igate != null then ", iGate " + (if .igate.connected then "connected to " + .igate.server else "not connected" end) else "" end)
           + ([(.channels // [])[] | ", channel " + .name + " rx " + (.rx_frames | tostring) + " tx " + (.tx_frames | tostring) + " bad FCS " + (.rx_bad_fcs | tostring)] | join(""))
           + (if .position_log != null then ", position log " + (if .position_log.enabled then "on" else "off" end) else "" end)
         else " (" + .api_error + ")" end)),
    line("radios"; (.radios | length | tostring) + " radio(s)"
      + ([(.radios // {}) | to_entries[] | ", " + .key + " " + (if .value.present then "present" else "unplugged" end) + (if .value.owner != "" then " (" + .value.owner + ")" else "" end)] | join(""))),
    line("time"; if .synced then "synced to " + .source + (if .source == "NTP" and .reference != "" then " " + .reference else "" end) + ", offset " + (.offset_ms | tostring) + " ms" else "not synced" end),
    line("release"; "DXBerry " + .dxberry + ", Graywolf " + (if .graywolf == "" then "-" else .graywolf end) + ", station " + (if .dxberryd == "" then "-" else .dxberryd end)),
    (if has("services") then
       (if .services.ok then "services:", ((.services.units // [])[] | "  " + .unit + ": " + (if .load == "not-found" then "not installed" else .active + " (" + .sub + ")" end))
        else "services: unavailable (" + (.services.error // "unknown error") + ")" end)
     else empty end)'
}
