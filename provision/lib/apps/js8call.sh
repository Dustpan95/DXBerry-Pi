#!/bin/bash
# shellcheck shell=bash
# JS8Call as a program on a radio (applications spec; ruling 4: one logging port for every radio).
# One session per radio, each with its own settings file ("JS8Call - RADIO.ini", Qt INI, section
# Configuration). The first fill and the wiring run as dxbapps (dxberry-app settings), so the
# file is written by its owner and nothing here changes ownership. The wiring writes only the
# sound card, rig control and PTT keys; everything else in the file stays the operator's.
# Expects common.sh, inifile.sh, apps.sh and radio.sh to be sourced.

app_js8call_kind() { echo radio; }
app_js8call_session() { echo yes; }
app_js8call_label() { echo JS8Call; }
app_js8call_multi() { echo yes; }
app_js8call_audio() { echo pulse; }
app_js8call_screen() { echo yes; }
app_js8call_needs_service_restart() { echo no; }
app_js8call_installed() { dxb_apps_installed js8call; }
app_js8call_version() { dxb_apps_version js8call; }
app_js8call_unit() { dxb_app_session_unit js8call "$1"; }
# nothing to undo: the settings file stays for the next time the radio is claimed
app_js8call_unwire() { return 0; }
app_js8call_settings_file() { printf '%s/.config/JS8Call - %s.ini\n' "$DXB_APPS_HOME" "$1"; }

# _dxb_js8call_set FILE KEY VALUE: one key of the Configuration section; 0 written or unchanged.
_dxb_js8call_set() {
  dxb_ini_set "$1" Configuration "$2" "$3"
  (( $? != 6 )) || { echo "js8call: could not write $2 in $1" >&2; return 6; }
}

# app_js8call_firstfill RADIO: call sign, grid (when known) and the logging broadcast to the one
# bridge port every radio shares.
app_js8call_firstfill() {
  local f st call grid
  f=$(app_js8call_settings_file "$1")
  st=$(dxb_apps_station) || return 6
  call=$(jq -r .call <<< "$st"); grid=$(jq -r .grid <<< "$st")
  mkdir -p "${f%/*}" || return 6
  _dxb_js8call_set "$f" MyCall "$call" || return 6
  [[ -z $grid ]] || _dxb_js8call_set "$f" MyGrid "$grid" || return 6
  _dxb_js8call_set "$f" BroadcastToN1MM true || return 6
  _dxb_js8call_set "$f" N1MMServer 127.0.0.1 || return 6
  _dxb_js8call_set "$f" N1MMServerPort 2333 || return 6
}

# app_js8call_wire RADIO: the sound card, rigctld and PTT of RADIO (these keys only, ever).
app_js8call_wire() {
  local f w pttm
  f=$(app_js8call_settings_file "$1")
  w=$(dxb_apps_wiring "$1") || return 6
  local dev; dev=$(jq -r .card <<< "$w")
  if [[ $(jq -r .ptt <<< "$w") == cat ]]; then pttm=PTT_method_CAT; else pttm=PTT_method_VOX; fi
  mkdir -p "${f%/*}" || return 6
  _dxb_js8call_set "$f" SoundInName "$(dxb_ini_qstring "$dev")" || return 6
  _dxb_js8call_set "$f" SoundOutName "$(dxb_ini_qstring "$dev")" || return 6
  _dxb_js8call_set "$f" Rig 'Hamlib NET rigctl' || return 6
  _dxb_js8call_set "$f" CATNetworkPort "$(jq -r .rigctld <<< "$w")" || return 6
  _dxb_js8call_set "$f" PTTMethod "$(dxb_ini_qenum TransceiverFactory::PTTMethod "$pttm")" || return 6
  _dxb_js8call_set "$f" PTTport '' || return 6
  _dxb_js8call_set "$f" Polling 1 || return 6
}

# app_js8call_command RADIO INDEX: the command line, one argument per line (runs as dxbapps, which
# reads no radio record).
app_js8call_command() {
  local exe="$DXB_APPS_OPT/js8call/squashfs-root/AppRun"
  [[ -z $(_dxb_apps_dpkg_version js8call) ]] || exe=/usr/bin/JS8Call
  printf '%s\n' "$exe" "--rig-name=$1"
}
