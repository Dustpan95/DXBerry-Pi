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
