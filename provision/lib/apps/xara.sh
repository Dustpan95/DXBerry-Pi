#!/bin/bash
# shellcheck shell=bash
app_xara_kind() { echo radio; }
app_xara_session() { echo yes; }
app_xara_label() { echo 'XARA HF (receive only)'; }
app_xara_multi() { echo no; }   # The local client ports are shared.
app_xara_audio() { echo pulse; }
app_xara_screen() { echo yes; }
app_xara_needs_service_restart() { echo no; }
app_xara_installed() { dxb_apps_installed xara; }
app_xara_version() { dxb_apps_version xara; }
app_xara_unit() { dxb_app_session_unit xara "$1"; }
app_xara_wire() { return 0; }
app_xara_unwire() { return 0; }
app_xara_bundled_kit() {
  [[ -r $DXB_SHARE/xara.zip ]] || return 1
  printf '%s/xara.zip\n' "$DXB_SHARE"
}
# The generic XARA builds omit the VarAC 17 feature. Only accept a release
# explicitly packaged for this mode and carrying GitHub's SHA-256 digest.
app_xara_remote_latest() {
  local releases kit
  releases=$(_dxb_apps_api "$DXB_GH_API/Dustpan95/xara/releases?per_page=100") || return 1
  kit=$(jq -c 'first(.[] | select(.draft == false and .prerelease == false)
    | .assets[] | select(.name | test("^xara-varac17-aarch64[.]zip$"))
    | {version:(.label // ""), asset:.name, type:"kit", url:.browser_download_url, sha256:.digest}) // empty' <<< "$releases") || return 1
  if [[ -z $kit ]]; then dxb_apps_source_latest local:xara; return; fi
  jq -ce 'select((.version | test("^[0-9]+[.][0-9]+[.][0-9]+([+][a-f0-9]{40})?$"))
    and (.sha256 | test("^sha256:[a-f0-9]{64}$")) and (.url | startswith("https://github.com/Dustpan95/xara/releases/download/")))
    | .sha256 |= ltrimstr("sha256:")' <<< "$kit"
}
app_xara_local_version() { python3 "$DXB_LIB/native-archive.py" xara "$1"; }
app_xara_unpack() { python3 "$DXB_LIB/native-archive.py" xara "$1" "$2"; }

app_xara_firstfill() {
  local dir=$DXB_APPS_HOME/.config/dxberry-xara call
  [[ ! -e $dir/$1.call ]] || return 0
  call=$(dxb_apps_station | jq -er .call) || return 6
  mkdir -p "$dir" && printf '%s\n' "${call:-N0CALL}" > "$dir/$1.call"
}

app_xara_command() {
  local call
  read -r call < "$DXB_APPS_HOME/.config/dxberry-xara/$1.call" || return 6
  printf '%s\n' xterm -title "XARA HF · $1 · RX only" -e env "PULSE_SOURCE=${1^^}.in" \
    "$DXB_APPS_OPT/xara/xara" --call "$call" --hf 500 --audio alsa --capture pulse --playback null \
    --addr 127.0.0.1 --port 8300 --kiss-port 8100
}
