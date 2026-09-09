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
        (["audio","cat","hid","ptt_serial"][] as $k | if ($r[$k] != null) and (($r[$k].path // "") == "") then bad($n + ": pin " + $k + " has no path") else empty end)
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
  local opts=$1 base=$2 k sel pin cand defaults='{}' r old_type new_type
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
