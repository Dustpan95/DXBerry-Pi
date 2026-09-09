#!/bin/bash
# shellcheck shell=bash
# Radio plumbing: discovery, the radio record, derived state, ownership. Spec: docs/design/2026-09-09-radio-plumbing.md

: "${DXB_SYSFS_ROOT:=/sys}"
: "${DXB_SHARE:=/opt/dxberry/share}"
: "${DXB_RADIO_PROFILES:=$DXB_SHARE/radio-profiles.tsv}"
: "${DXB_RADIOS_FILE:=$DXB_STATE_DIR/radios.json}"
: "${DXB_RUN_DIR:=/run/dxberry}"
: "${DXB_RADIOS_STATE:=$DXB_RUN_DIR/radios-state.json}"
: "${DXB_APPS_DIR:=${DXB_LIB:-/opt/dxberry/lib}/apps}"
# DXB_RADIO_SCAN / DXB_RADIOS are the module's public interface state, filled by
# dxb_radio_scan_cache and (in a later task) dxb_radio_load - read by other modules, not this one.
# shellcheck disable=SC2034
DXB_RADIO_SCAN='[]'
# shellcheck disable=SC2034
DXB_RADIOS=''

# ---- profiles ------------------------------------------------------------------------------
dxb_radio_profiles_json() {
  [[ -f $DXB_RADIO_PROFILES ]] || { echo '[]'; return 0; }
  awk -F'\t' '
    /^[[:blank:]]*#/ || NF < 7 { next }
    { printf "%s{\"vidpid\":\"%s\",\"name\":\"%s\",\"ptt\":\"%s\",\"ptt_type\":\"%s\",\"cat\":\"%s\",\"model\":%d,\"baud\":%d}", (n++ ? "," : "["), $1, $2, $3, $4, $5, $6, $7 }
    END { print (n ? "]" : "[]") }' "$DXB_RADIO_PROFILES"
}

# ---- scanner -------------------------------------------------------------------------------
_dxb_radio_attr() { local v=''; [[ -f $1/$2 ]] && read -r v < "$1/$2"; printf '%s' "$v"; }

# _dxb_radio_usb_ctx RESOLVED_SYSFS_PATH: prints "IFACE_PORT DEV_PORT DEVDIR" for the USB interface
# and device the node hangs off (udev ID_PATH style: 1-1.3:1.0 -> usb-0:1.3:1.0), or returns 1.
_dxb_radio_usb_ctx() {
  local acc='' comp iface='' dev='' devdir='' rest=$1
  while [[ -n $rest ]]; do
    comp=${rest%%/*}
    if [[ $rest == */* ]]; then rest=${rest#*/}; else rest=''; fi
    [[ -n $comp ]] || continue
    acc="$acc/$comp"
    if [[ $comp =~ ^[0-9]+-[0-9.]+:[0-9]+\.[0-9]+$ ]]; then iface=$comp
    elif [[ $comp =~ ^[0-9]+-[0-9.]+$ ]]; then dev=$comp; devdir=$acc; fi
  done
  [[ -n $iface && -n $dev ]] || return 1
  printf '%s %s %s\n' "usb-0:${iface#*-}" "usb-0:${dev#*-}" "$devdir"
}

# dxb_radio_scan: JSON array of USB candidates grouped by device port (spec section 5.1).
dxb_radio_scan() {
  local d real kernel kind ifport devport devdir funcs='[]'
  for d in "$DXB_SYSFS_ROOT"/class/sound/card* "$DXB_SYSFS_ROOT"/class/tty/ttyUSB* "$DXB_SYSFS_ROOT"/class/tty/ttyACM* "$DXB_SYSFS_ROOT"/class/hidraw/hidraw*; do
    [[ -e $d ]] || continue
    kernel=${d##*/}
    case $d in */class/sound/*) kind=audio ;; */class/tty/*) kind=serial ;; *) kind=hid ;; esac
    real=$(readlink -f "$d") || continue
    read -r ifport devport devdir < <(_dxb_radio_usb_ctx "$real") || continue
    [[ -n ${ifport:-} ]] || continue
    funcs=$(jq -c --arg kind "$kind" --arg kernel "$kernel" --arg path "$ifport" --arg dev "$devport" \
      --arg vid "$(_dxb_radio_attr "$devdir" idVendor)" --arg pid "$(_dxb_radio_attr "$devdir" idProduct)" \
      --arg serial "$(_dxb_radio_attr "$devdir" serial)" --arg product "$(_dxb_radio_attr "$devdir" product)" \
      '. + [{kind: $kind, kernel: $kernel, path: $path, dev: $dev, vidpid: ($vid + ":" + $pid), serial: $serial, product: $product}]' <<< "$funcs")
  done
  jq -c --argjson profiles "$(dxb_radio_profiles_json)" '
    group_by(.dev) | sort_by(.[0].dev) | to_entries | map(
      .value as $f
      | ($f | (if any(.kind == "audio") then . else map(select(.kind != "hid")) end) | sort_by(.path) | map(del(.dev))) as $fn
      | ($fn | map(select(.kind == "audio")) | .[0].vidpid // "") as $a
      | ($fn | map(select(.kind == "serial")) | .[0].vidpid // "") as $s
      | (($profiles | map(select(.vidpid == $a)) | .[0]) // ($profiles | map(select(.vidpid == $s)) | .[0]) // null) as $p
      | {index: (.key + 1), port: $f[0].dev,
         profile: ($p.vidpid // "generic"),
         name: ($p.name // (if $a != "" then "Unknown USB audio device" else "Unknown USB serial device" end)),
         defaults: (if $p then ($p | {ptt, ptt_type, cat, model, baud})
                    else {ptt: (if $s != "" then "rigctld" else "vox" end), ptt_type: "NONE", cat: (if $s != "" then "same" else "none" end), model: 1, baud: 0} end),
         functions: $fn})
    | map(select(.functions | length > 0))' <<< "$funcs"
}
# shellcheck disable=SC2034
dxb_radio_scan_cache() { DXB_RADIO_SCAN=$(dxb_radio_scan) || DXB_RADIO_SCAN='[]'; }

# ---- record --------------------------------------------------------------------------------
DXB_RADIO_NAME_RE='^[a-z][a-z0-9]{0,11}$'
dxb_radio_empty_record() { printf '{"version":1,"radios":{},"gps":{"device":"auto","baud":9600,"pps":""}}\n'; }

dxb_radio_load() {
  if [[ -f $DXB_RADIOS_FILE ]]; then
    DXB_RADIOS=$(jq -c . "$DXB_RADIOS_FILE" 2> /dev/null) || { dxb_error "$DXB_RADIOS_FILE is not valid JSON"; return 6; }
  else
    DXB_RADIOS=$(dxb_radio_empty_record)
  fi
}

# dxb_radio_validate JSON: 0 valid, 2 invalid (reasons on stderr).
dxb_radio_validate() {
  local errs
  errs=$(jq -r '
    def bad(m): "  " + m;
    [ (if .version != 1 then bad("version must be 1") else empty end),
      (.radios | to_entries[] | .key as $n | .value as $r |
        (if ($n | test("^[a-z][a-z0-9]{0,11}$") | not) then bad("bad radio name " + $n) else empty end),
        (if ($r.ptt.method as $m | ["rigctld","cm108","gpio","vox","digirig_tone","none"] | index($m)) == null then bad($n + ": bad ptt method") else empty end),
        (if ($r.rig.ptt_type as $t | ["RIG","RTS","DTR","NONE"] | index($t)) == null then bad($n + ": bad ptt_type") else empty end),
        (if ($r.wiring as $w | ["full","names"] | index($w)) == null then bad($n + ": bad wiring") else empty end),
        (if ($r.rig.model | type) != "number" or $r.rig.model < 1 then bad($n + ": bad model") else empty end),
        (if ($r.rigctld_port | type) != "number" or $r.rigctld_port < 4532 or ($r.rigctld_port % 2) != 0 then bad($n + ": bad port") else empty end),
        (if ($r.owner | type) != "string" then bad($n + ": bad owner") else empty end),
        (if (($r.label | type) == "string" and ($r.label | length) <= 40 and ($r.label | test("^[ -~]*$"))) then empty else bad($n + ": bad label") end),
        (["audio","cat","hid","ptt_serial"][] as $k | if ($r[$k] != null) and (($r[$k].path // "") == "") then bad($n + ": pin " + $k + " has no path") else empty end),
        (if (["audio","cat","hid","ptt_serial"] | map($r[.]) | map(select(. != null)) | length) == 0 then bad($n + ": needs at least one pinned function") else empty end)
      ),
      (if ([.radios[].rigctld_port] | unique | length) != ([.radios[].rigctld_port] | length) then bad("duplicate rigctld ports") else empty end)
    ] | .[]' <<< "$1" 2>&1)
  [[ -z $errs ]] && return 0
  dxb_error "invalid radio record:"; printf '%s\n' "$errs" >&2
  return 2
}

dxb_radio_save() {
  local j
  j=$(jq -S . <<< "$1" 2> /dev/null) || return 2
  dxb_radio_validate "$j" || return 2
  mkdir -p "$(dirname "$DXB_RADIOS_FILE")" 2> /dev/null
  dxb_write_if_changed "$DXB_RADIOS_FILE" "$j" 600
  [[ -f $DXB_RADIOS_FILE && $(< "$DXB_RADIOS_FILE") == "$j" ]] || { dxb_error "could not write $DXB_RADIOS_FILE"; return 6; }
  DXB_RADIOS=$(jq -c . <<< "$j")
}

dxb_radio_names() { jq -r '.radios | keys[]' <<< "$DXB_RADIOS"; }
dxb_radio_get() { jq -ce --arg n "$1" '.radios[$n] // empty' <<< "$DXB_RADIOS" || { dxb_error "no such radio: $1"; return 3; }; }
dxb_radio_alloc_port() { jq -r '[.radios[].rigctld_port] as $u | [range(4532; 4600; 2)] | map(select(. as $p | $u | index($p) | not)) | .[0]' <<< "$DXB_RADIOS"; }

# _dxb_radio_pin SELECTOR KIND: resolves "N" / "N:K" against DXB_RADIO_SCAN to a pin object; "none" -> null.
_dxb_radio_pin() {
  local sel=$1 kind=$2 n k
  [[ $sel == none || -z $sel ]] && { echo null; return 0; }
  [[ $sel =~ ^([0-9]+)(:([0-9]+))?$ ]] || { dxb_error "bad candidate selector '$sel' (use N or N:K)"; return 2; }
  n=${BASH_REMATCH[1]}; k=${BASH_REMATCH[3]:-1}
  (( k >= 1 )) || { dxb_error "bad candidate selector '$sel' (K must be >= 1)"; return 2; }
  jq -ce --argjson n "$n" --argjson k "$k" --arg kind "$kind" \
    '(.[] | select(.index == $n) | .functions | map(select(.kind == $kind)) | .[$k - 1]) // empty | {path, vidpid, serial}' <<< "$DXB_RADIO_SCAN" \
    || { dxb_error "candidate $sel has no $kind function"; return 2; }
}

# _dxb_radio_build OPTS BASE: merge OPTS (candidate selectors + overrides) into BASE (an existing radio or {}).
_dxb_radio_build() {
  local opts=$1 base=$2 k sel pin cand defaults='{}' r old_type new_type old_model
  r=$base
  for k in audio cat hid ptt_serial; do
    sel=$(jq -r --arg k "$k" '.[$k] // empty' <<< "$opts")
    [[ -n $sel ]] || continue
    pin=$(_dxb_radio_pin "$sel" "$( [[ $k == audio ]] && echo audio || { [[ $k == hid ]] && echo hid || echo serial; } )") || return 2
    r=$(jq -c --arg k "$k" --argjson p "$pin" '.[$k] = $p' <<< "$r")
  done
  # profile defaults come from the audio candidate, else the cat candidate, only when creating
  if [[ $(jq -r '.profile // empty' <<< "$r") == "" ]]; then
    # the profile candidate is the audio selector unless it is absent or "none", then the cat selector
    cand=$(jq -r 'if (.audio // "none") != "none" then .audio else (.cat // empty) end' <<< "$opts"); cand=${cand%%:*}
    if [[ -n $cand && $cand != none ]]; then
      defaults=$(jq -c --argjson n "$cand" '.[] | select(.index == $n) | {profile: .profile, defaults: .defaults}' <<< "$DXB_RADIO_SCAN")
    fi
    [[ -n $defaults ]] || defaults='{}'
    r=$(jq -c --argjson d "$defaults" '
      ($d.defaults // {ptt: "vox", ptt_type: "NONE", model: 1, baud: 0}) as $df
      | {label: "", profile: ($d.profile // "generic"), audio: null, cat: null, hid: null, ptt_serial: null,
         ptt: {method: $df.ptt, gpio_line: null}, rig: {model: $df.model, baud: $df.baud, ptt_type: $df.ptt_type},
         wiring: "full", owner: ""} + .' <<< "$r")
    # the DigiRig hid pin is implied by the audio candidate when the operator did not choose one
    if [[ $(jq -r '.hid' <<< "$r") == null ]]; then
      cand=$(jq -r '.audio // empty' <<< "$opts"); cand=${cand%%:*}
      [[ -n $cand && $cand != none ]] && pin=$(_dxb_radio_pin "$cand" hid 2> /dev/null) && r=$(jq -c --argjson p "$pin" '.hid = $p' <<< "$r")
    fi
  fi
  r=$(jq -c --argjson o "$opts" '
    . + (if $o.label != null then {label: $o.label} else {} end)
      + (if $o.wiring != null then {wiring: $o.wiring} else {} end)
    | .ptt.method = ($o.ptt // .ptt.method) | .ptt.gpio_line = (if $o.gpio_line != null then $o.gpio_line else .ptt.gpio_line end)
    | .rig.model = ($o.model // .rig.model) | .rig.baud = ($o.baud // .rig.baud) | .rig.ptt_type = ($o.ptt_type // .rig.ptt_type)' <<< "$r")
  # rigctld's ptt_type must not point at a serial line or CAT socket that has no pin behind it
  old_type=$(jq -r '.rig.ptt_type' <<< "$r")
  r=$(jq -c '
    if .cat == null and .rig.ptt_type == "RIG" then .rig.ptt_type = "NONE"
    elif .cat == null and .ptt_serial == null and (.rig.ptt_type == "RTS" or .rig.ptt_type == "DTR") then .rig.ptt_type = "NONE"
    else . end' <<< "$r")
  new_type=$(jq -r '.rig.ptt_type' <<< "$r")
  [[ $old_type == "$new_type" ]] || dxb_warn "radio: ptt_type $old_type needs a serial pin; set to NONE (pin --cat or --ptt-serial, then --ptt-type $old_type)"
  # a radio with no CAT pin has no rig to drive: hamlib's dummy model keeps PTT and the NET
  # rigctl interface uniform for every application (spec section 7.3)
  old_model=$(jq -r '.rig.model' <<< "$r")
  r=$(jq -c 'if .cat == null then .rig.model = 1 else . end' <<< "$r")
  [[ $old_model == $(jq -r '.rig.model' <<< "$r") ]] \
    || dxb_warn "radio: model $old_model needs a CAT pin; using the dummy model 1 (pin --cat, then --model $old_model)"
  printf '%s\n' "$r"
}

dxb_radio_add() {
  local name=$1 opts=$2 radio
  [[ $name =~ $DXB_RADIO_NAME_RE ]] || { dxb_error "radio name must match $DXB_RADIO_NAME_RE"; return 2; }
  jq -e --arg n "$name" '.radios[$n]' <<< "$DXB_RADIOS" > /dev/null 2>&1 && { dxb_error "radio $name already exists"; return 2; }
  jq -e 'type == "object"' <<< "$opts" > /dev/null 2>&1 || { dxb_error "options must be a JSON object"; return 2; }
  radio=$(_dxb_radio_build "$opts" '{}') || return 2
  radio=$(jq -c --argjson p "$(dxb_radio_alloc_port)" '. + {rigctld_port: $p, owner: ""}' <<< "$radio")
  dxb_radio_save "$(jq -c --arg n "$name" --argjson r "$radio" '.radios[$n] = $r' <<< "$DXB_RADIOS")" && dxb_info "radio $name added"
}

dxb_radio_set() {
  local name=$1 opts=$2 cur radio
  cur=$(dxb_radio_get "$name") || return 3
  radio=$(_dxb_radio_build "$opts" "$cur") || return 2
  dxb_radio_save "$(jq -c --arg n "$name" --argjson r "$radio" '.radios[$n] = $r' <<< "$DXB_RADIOS")" || return $?
  dxb_info "radio $name updated"
}

dxb_radio_remove() {
  dxb_radio_get "$1" > /dev/null || return 3
  dxb_radio_save "$(jq -c --arg n "$1" 'del(.radios[$n])' <<< "$DXB_RADIOS")" && dxb_info "radio $1 removed"
}

_dxb_radio_set_owner() { dxb_radio_save "$(jq -c --arg n "$1" --arg a "$2" '.radios[$n].owner = $a' <<< "$DXB_RADIOS")"; }

# ---- presence and runtime state ------------------------------------------------------------
_dxb_radio_kernel_for() { jq -r --arg p "$1" --arg k "$2" '[.[].functions[] | select(.path == $p and .kind == $k)] | .[0].kernel // empty' <<< "$DXB_RADIO_SCAN"; }

# dxb_radio_kernel_names NAME: {"audio":"card1","cat":"ttyUSB0","hid":null,"ptt_serial":null} for this boot.
dxb_radio_kernel_names() {
  local r k kind path out='{}'
  r=$(dxb_radio_get "$1") || return 3
  for k in audio cat hid ptt_serial; do
    case $k in audio) kind=audio ;; hid) kind=hid ;; *) kind=serial ;; esac
    path=$(jq -r --arg k "$k" '.[$k].path // empty' <<< "$r")
    if [[ -n $path ]]; then
      out=$(jq -c --arg k "$k" --arg v "$(_dxb_radio_kernel_for "$path" "$kind")" '.[$k] = (if $v == "" then null else $v end)' <<< "$out")
    else
      out=$(jq -c --arg k "$k" '.[$k] = null' <<< "$out")
    fi
  done
  printf '%s\n' "$out"
}

# dxb_radio_present NAME: 0 when every pinned function was found in the current scan, else 4.
dxb_radio_present() {
  local r names
  r=$(dxb_radio_get "$1") || return 3
  names=$(dxb_radio_kernel_names "$1")
  jq -e --argjson n "$names" '[["audio","cat","hid","ptt_serial"][] as $k | select(.[$k] != null) | $n[$k]] | all(. != null)' <<< "$r" > /dev/null || return 4
}

dxb_radio_wire_hash() { jq -c '{audio, cat, hid, ptt_serial, ptt, rig, rigctld_port, wiring}' <<< "$1" | sha256sum | cut -c1-16; }

# dxb_radio_write_state: the runtime mirror the console and status read.
dxb_radio_write_state() {
  local n out='{}' present names content
  for n in $(dxb_radio_names); do
    if dxb_radio_present "$n"; then present=true; else present=false; fi
    names=$(dxb_radio_kernel_names "$n")
    out=$(jq -c --arg n "$n" --argjson p "$present" --argjson k "$names" --arg s "$(dxb_rigctld_state "$n")" \
      --arg h "$(dxb_radio_wire_hash "$(dxb_radio_get "$n")")" --arg w "$(jq -r --arg n "$n" '.radios[$n].wired_hash // ""' "$DXB_RADIOS_STATE" 2> /dev/null)" \
      '.[$n] = {present: $p, kernel: $k, rigctld: $s, wire_hash: $h, wired_hash: $w}' <<< "$out")
  done
  mkdir -p "$(dirname "$DXB_RADIOS_STATE")" 2> /dev/null
  content=$(jq -c --argjson r "$out" '{generated: (now | todate), radios: $r}' <<< '{}')
  dxb_write_if_changed "$DXB_RADIOS_STATE" "$content" 644
  [[ -f $DXB_RADIOS_STATE && $(< "$DXB_RADIOS_STATE") == "$content" ]] || { dxb_error "could not write $DXB_RADIOS_STATE"; return 6; }
  return 0
}
# _dxb_radio_mark_wired NAME HASH: remember that the owner was wired with these inputs. 0 ok, 6 error.
_dxb_radio_mark_wired() {
  local n=$1 h=$2 cur j
  cur=$(jq -c . "$DXB_RADIOS_STATE" 2> /dev/null) || cur='{"radios":{}}'
  j=$(jq -c --arg n "$n" --arg h "$h" '.radios[$n].wired_hash = $h' <<< "$cur") || return 6
  mkdir -p "$(dirname "$DXB_RADIOS_STATE")" 2> /dev/null
  dxb_write_if_changed "$DXB_RADIOS_STATE" "$j" 644
  [[ -f $DXB_RADIOS_STATE && $(< "$DXB_RADIOS_STATE") == "$j" ]] || { dxb_error "could not write $DXB_RADIOS_STATE"; return 6; }
  return 0
}

# dxb_radio_apply [hotplug]: derive everything from the record. 0 ok, 6 derived-state error, 7 re-wire error.
# udev can start a hotplug apply at any moment, so the whole run is serialized on
# $DXB_RUN_DIR/apply.lock (fd 9); a second apply waits there instead of interleaving its writes.
# Without flock (or without the lock file) the run goes ahead with a warning: apply is idempotent,
# so the worst an interleaved run can do is write the same files twice.
# shellcheck disable=SC2120,SC2119  # called with no argument (default "full") from provision_radio and the CLI's "apply"; only "hotplug" passes one
dxb_radio_apply() {
  local mode=${1:-full} rc
  mkdir -p "$DXB_RUN_DIR" 2> /dev/null
  if ! exec 9> "$DXB_RUN_DIR/apply.lock"; then
    dxb_warn "could not open $DXB_RUN_DIR/apply.lock; applying without the apply lock"
    _dxb_radio_apply "$mode"
    return $?
  fi
  if command -v flock > /dev/null 2>&1; then
    flock 9 || dxb_warn "could not lock $DXB_RUN_DIR/apply.lock; applying without the apply lock"
  else
    dxb_warn "flock is not installed; a concurrent hotplug apply could interleave with this one"
  fi
  _dxb_radio_apply "$mode"; rc=$?
  exec 9>&-
  return $rc
}

_dxb_radio_apply() {
  local mode=${1:-full} n r present rc=0 wrc h wired owner
  dxb_radio_load || return 6
  dxb_radio_scan_cache
  if [[ $mode != hotplug ]]; then
    dxb_radio_udev_write "$DXB_RADIOS"; wrc=$?; (( wrc == 6 )) && rc=6
    dxb_radio_modprobe_install > /dev/null; (( $? == 6 )) && rc=6
  fi
  for n in $(dxb_radio_names); do
    r=$(dxb_radio_get "$n")
    if dxb_radio_present "$n"; then present=1; else present=0; dxb_info "radio $n: device absent"; fi
    dxb_rigctld_sync "$n" "$r" "$present" || rc=6
  done
  # shellcheck disable=SC2046
  dxb_rigctld_stop_all_except $(dxb_radio_names)
  dxb_radio_write_state || rc=6
  for n in $(dxb_radio_names); do
    r=$(dxb_radio_get "$n"); owner=$(jq -r '.owner' <<< "$r")
    if [[ -z $owner ]] || ! dxb_radio_present "$n"; then continue; fi
    h=$(dxb_radio_wire_hash "$r"); wired=$(jq -r --arg n "$n" '.radios[$n].wired_hash // ""' "$DXB_RADIOS_STATE")
    [[ $h == "$wired" ]] && continue
    if declare -F dxb_app_rewire > /dev/null; then
      if dxb_app_rewire "$n"; then
        _dxb_radio_mark_wired "$n" "$h" || dxb_warn "radio $n: could not record the wiring hash; it will be re-wired on the next apply"
      else
        dxb_error "radio $n: re-wiring owner $owner failed"; (( rc == 0 )) && rc=7
      fi
    fi
  done
  return $rc
}

# ---- applications --------------------------------------------------------------------------
dxb_app_list() { local f; for f in "$DXB_APPS_DIR"/*.sh; do [[ -f $f ]] || continue; f=${f##*/}; echo "${f%.sh}"; done; }
dxb_app_load() {
  [[ $1 =~ ^[a-z][a-z0-9_]{0,15}$ && -f $DXB_APPS_DIR/$1.sh ]] || { dxb_error "no such application: $1 (available: $(dxb_app_list | tr '\n' ' '))"; return 3; }
  # shellcheck disable=SC1090
  source "$DXB_APPS_DIR/$1.sh"
}
dxb_app_owned() { jq -r --arg a "$1" '[.radios[] | select(.owner == $a)] | length' <<< "$DXB_RADIOS"; }
dxb_app_unit() { "app_$1_unit"; }
dxb_app_wire() {
  [[ $(jq -r --arg n "$2" '.radios[$n].wiring' <<< "$DXB_RADIOS") == names ]] && return 0
  "app_$1_wire" "$2" || return 7
  if [[ $("app_$1_needs_service_restart") == yes ]]; then systemctl restart "$(dxb_app_unit "$1")" || return 7; fi
  return 0
}
dxb_app_unwire() { [[ $(jq -r --arg n "$2" '.radios[$n].wiring' <<< "$DXB_RADIOS") == names ]] && return 0; "app_$1_unwire" "$2"; }
dxb_app_start() {
  local u; u=$(dxb_app_unit "$1")
  systemctl is-active --quiet "$u" || systemctl start "$u" || { dxb_error "could not start $u"; return 5; }
  if declare -F "app_$1_wait_ready" > /dev/null; then "app_$1_wait_ready" || { dxb_error "$u did not become ready"; return 5; }; fi
  return 0
}
dxb_app_stop_if_idle() { (( $(dxb_app_owned "$1") == 0 )) || return 0; systemctl stop "$(dxb_app_unit "$1")" && dxb_info "$1 stopped (owns no radio)"; }

# dxb_app_rewire NAME: re-run the current owner's wiring (apply calls this when the inputs changed).
dxb_app_rewire() {
  local owner; owner=$(jq -r --arg n "$1" '.radios[$n].owner // ""' <<< "$DXB_RADIOS")
  [[ -n $owner ]] || return 0
  dxb_app_load "$owner" || return 3
  dxb_app_wire "$owner" "$1"
}

# dxb_radio_claim NAME APP (spec section 9.2). 0 ok, 3 unknown, 4 absent, 5 failed (released), 6 record error.
# rigctld is never touched here (spec 9.2): it belongs to the radio, not the hand-over, so this
# function only ever runs the two apps' unit and app_<app>_* functions, never dxb_radio_write_state.
dxb_radio_claim() {
  local name=$1 app=$2 cur
  dxb_radio_get "$name" > /dev/null || return 3
  dxb_app_load "$app" || return 3
  dxb_radio_present "$name" || { dxb_error "radio $name is not plugged in"; return 4; }
  cur=$(jq -r --arg n "$name" '.radios[$n].owner' <<< "$DXB_RADIOS")
  if [[ $cur == "$app" ]]; then
    dxb_app_wire "$app" "$name" || return 5
    _dxb_radio_mark_wired "$name" "$(dxb_radio_wire_hash "$(dxb_radio_get "$name")")" \
      || dxb_warn "radio $name: could not record the wiring hash; it will be re-wired on the next apply"
    return 0
  fi
  if [[ -n $cur ]]; then
    if dxb_app_load "$cur"; then dxb_app_unwire "$cur" "$name"; fi
    _dxb_radio_set_owner "$name" "" || return 6
    dxb_app_load "$cur" 2> /dev/null && dxb_app_stop_if_idle "$cur"
    dxb_info "radio $name released by $cur"
  fi
  _dxb_radio_set_owner "$name" "$app" || return 6
  if ! dxb_app_start "$app" || ! dxb_app_wire "$app" "$name"; then
    dxb_app_unwire "$app" "$name"
    _dxb_radio_set_owner "$name" ""
    dxb_app_stop_if_idle "$app"
    dxb_error "radio $name: $app could not take it; left released"
    return 5
  fi
  _dxb_radio_mark_wired "$name" "$(dxb_radio_wire_hash "$(dxb_radio_get "$name")")" \
    || dxb_warn "radio $name: could not record the wiring hash; it will be re-wired on the next apply"
  dxb_info "radio $name now owned by $app"
}

# dxb_radio_release NAME (spec section 9.2, steps 3-4 with an empty owner). rigctld untouched, as above.
dxb_radio_release() {
  local name=$1 cur
  dxb_radio_get "$name" > /dev/null || return 3
  cur=$(jq -r --arg n "$name" '.radios[$n].owner' <<< "$DXB_RADIOS")
  [[ -n $cur ]] || return 0
  dxb_app_load "$cur" && dxb_app_unwire "$cur" "$name"
  _dxb_radio_set_owner "$name" "" || return 6
  dxb_app_load "$cur" 2> /dev/null && dxb_app_stop_if_idle "$cur"
  dxb_info "radio $name released"
}

# ---- provisioning step ---------------------------------------------------------------------
DXB_RADIO_PACKAGES='libhamlib-utils gpsd gpsd-clients chrony alsa-utils'
# Set in run mode when dxb_gps_boot_config changed config.txt (spec section 10); on first boot
# the reboot at the end of the run happens regardless, so this is never set there.
# shellcheck disable=SC2034
DXB_RADIO_REBOOT_NEEDED=0

# provision_radio (spec section 10): packages, units, modprobe pinning, gpsd/chrony, time
# handoff from systemd-timesyncd to chrony, derived radio state, and the status lines. A failed
# apt install is a failed step named "radio", never fatal - the rest of the step still runs.
provision_radio() {
  local n present=0 total=0 gps_state
  # shellcheck disable=SC2086
  if ! DEBIAN_FRONTEND=noninteractive apt-get install -y $DXB_RADIO_PACKAGES > /dev/null 2>&1; then
    dxb_step_failed radio "could not install $DXB_RADIO_PACKAGES (no network?); rigctld and gpsd will be missing until a re-run"
  fi
  dxb_rigctld_install_units; (( $? == 6 )) && dxb_step_failed radio "could not install the rigctld or hotplug units"
  dxb_radio_modprobe_install > /dev/null; (( $? == 6 )) && dxb_step_failed radio "could not write $DXB_MODPROBE_FILE"
  systemctl disable --now systemd-timesyncd > /dev/null 2>&1 || true
  systemctl mask systemd-timesyncd > /dev/null 2>&1 || true
  systemctl enable chrony > /dev/null 2>&1 || dxb_step_failed radio "could not enable chrony"
  dxb_gps_configure
  if dxb_gps_boot_config; then
    if [[ ${DXB_MODE:-run} == run ]]; then
      # shellcheck disable=SC2034  # read by dxberry-provision after sourcing this file
      DXB_RADIO_REBOOT_NEEDED=1
      dxb_status_add "gps: reboot required (config.txt changed for GPS_DEVICE=${DXB_CFG[GPS_DEVICE]}${DXB_CFG[GPS_PPS]:+, PPS})"
    fi
  fi
  dxb_radio_apply
  case $? in
    6) dxb_step_failed radio "apply failed; see $DXB_LOG_FILE" ;;
    7) dxb_step_failed radio "an owner could not be re-wired; see $DXB_LOG_FILE" ;;
  esac
  for n in $(dxb_radio_names); do
    total=$(( total + 1 ))
    dxb_radio_present "$n" && present=$(( present + 1 ))
  done
  dxb_status_add "radio: $total radios, $present present (manage with: sudo dxberry-radio scan)"
  if (( DXB_CFG[_GPS] )); then gps_state=$(dxb_gps_status_line); else gps_state='gps: off (GPS_DEVICE=none)'; fi
  dxb_status_add "$gps_state"
  dxb_status_add "time: chrony (gps $( [[ $gps_state == *fix* && $gps_state != *"no fix"* ]] && echo present || echo absent ))"
  return 0
}
