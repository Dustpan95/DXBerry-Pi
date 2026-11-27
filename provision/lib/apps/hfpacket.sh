#!/bin/bash
# shellcheck shell=bash
app_hfpacket_kind() { echo radio; }
app_hfpacket_session() { echo yes; }
app_hfpacket_label() { echo "HF Packet"; }
app_hfpacket_multi() { echo no; }
app_hfpacket_audio() { echo alsa; }
app_hfpacket_screen() { echo yes; }
app_hfpacket_needs_service_restart() { echo no; }
app_hfpacket_installed() { dxb_apps_installed hfpacket; }
app_hfpacket_version() { dxb_apps_version hfpacket; }
app_hfpacket_unit() { dxb_app_session_unit hfpacket "$1"; }
app_hfpacket_unwire() { return 0; }
app_hfpacket_settings_file() { printf '%s/.config/dxberry-hfpacket/%s.conf\n' "$DXB_APPS_HOME" "$1"; }

app_hfpacket_wire() {
  local f w call input output ptt
  f=$(app_hfpacket_settings_file "$1")
  w=$(dxb_apps_wiring "$1") || return 6
  call=$(dxb_apps_station | jq -er .call) || return 6
  input=$(jq -r .device <<< "$w"); output=$input; ptt=$(jq -r .ptt <<< "$w")
  if [[ -z $call || $ptt == none ]]; then output=null; ptt=none; fi
  mkdir -p "${f%/*}" || return 6
  {
    printf 'ADEVICE %s %s\nCHANNEL 0\nMYCALL %s\nMODEM 300\n' "$input" "$output" "${call:-NOCALL}"
    if [[ $ptt == cat ]]; then printf 'PTT RIG 2 %s\n' "$(jq -r .rigctld <<< "$w")"; fi
    printf 'AGWPORT 0\nKISSPORT 8311\n'
  } > "$f"
}

app_hfpacket_firstfill() {
  [[ -f $(app_hfpacket_settings_file "$1") ]] || app_hfpacket_wire "$1"
}

app_hfpacket_command() {
  printf '%s\n' xterm -title "HF Packet · $1" -e /opt/dxberry/bin/dxberry-hfpacket-terminal "$(app_hfpacket_settings_file "$1")"
}
