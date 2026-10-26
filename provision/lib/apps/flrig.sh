#!/bin/bash
# shellcheck shell=bash
# flrig as a radio's rig control (applications spec; it is not an owner: kind rigcontrol). The
# radio's record says rig.control = flrig; apply then runs one session, dxberry-screen@flrig-RADIO,
# next to the owner's. The operator picks the transceiver and its serial port inside flrig (the card
# shows the port as /dev/dxberry/RADIO-cat); rigctld talks to flrig through Hamlib model 4, and the
# owner's program talks to flrig or rigctld as its own module wires it.
# The one thing DXBerry writes is flrig's XML-RPC port, per radio. flrig keeps that key (xmlport) in
# the transceiver's own preferences file, <config-dir>/<xcvr_name>.prefs, where xcvr_name comes from
# flrig.prefs (NONE until the operator chooses one), and reads that file only when it has a version
# entry. The operator can switch transceivers inside flrig, so the wiring runs on every start
# (app_flrig_wire_each_start). The port itself is read once at startup.
# The wiring runs as dxbapps (dxberry-app settings), so nothing here changes ownership.
# Expects common.sh, inifile.sh, apps.sh and radio.sh to be sourced.

app_flrig_kind() { echo rigcontrol; }
app_flrig_session() { echo yes; }
app_flrig_label() { echo flrig; }
app_flrig_multi() { echo yes; }
# flrig opens no audio: its session leaves the radio's sound server alone
app_flrig_audio() { echo none; }
app_flrig_screen() { echo yes; }
app_flrig_needs_service_restart() { echo no; }
app_flrig_installed() { dxb_apps_installed flrig; }
app_flrig_version() { dxb_apps_version flrig; }
app_flrig_unit() { dxb_app_session_unit flrig "$1"; }
app_flrig_unwire() { return 0; }
# the radio's wiring must follow the operator's choice of transceiver, so it runs at every start
app_flrig_wire_each_start() { echo yes; }
_dxb_flrig_dir() { printf '%s/.flrig-%s\n' "$DXB_APPS_HOME" "$1"; }
# the file the page shows; the port lives in the transceiver's own file (see app_flrig_wire)
app_flrig_settings_file() { printf '%s/flrig.prefs\n' "$(_dxb_flrig_dir "$1")"; }

# _dxb_flrig_xcvr DIR: the transceiver name flrig.prefs records (NONE when unset or not a plain
# file name: it becomes a file name here).
_dxb_flrig_xcvr() {
  local n
  n=$(dxb_prefs_get "$1/flrig.prefs" xcvr_name 2> /dev/null)
  if [[ -z $n || $n == */* || $n == .* ]]; then n=NONE; fi
  printf '%s\n' "$n"
}

# _dxb_flrig_version: flrig's own version number (the Debian version without epoch and revision),
# 2.0 when it cannot be told.
_dxb_flrig_version() {
  local v
  v=$(dxb_apps_version flrig 2> /dev/null); v=${v#*:}
  [[ $v =~ ^[0-9]+(\.[0-9]+)*$ ]] || { [[ $v =~ ^([0-9]+(\.[0-9]+)*) ]] && v=${BASH_REMATCH[1]} || v=2.0; }
  printf '%s\n' "$v"
}

# app_flrig_wire RADIO: RADIO's XML-RPC port (12345 + the radio's index) in the transceiver's own
# preferences file (this key, and a version entry when the file has none, so flrig reads it).
app_flrig_wire() {
  local dir w x f rc
  dir=$(_dxb_flrig_dir "$1")
  w=$(dxb_apps_wiring "$1") || return 6
  x=$(_dxb_flrig_xcvr "$dir"); f="$dir/$x.prefs"
  mkdir -p "$dir" || return 6
  if [[ ! -f $f ]]; then
    printf '; FLTK preferences file format 1.0\n; vendor: w1hkj.com\n; application: %s\n\n[.]\n\n' "$x" > "$f" \
      || { echo "flrig: could not write $f" >&2; return 6; }
  fi
  dxb_prefs_set "$f" xmlport "$(jq -r .flrig_port <<< "$w")"; rc=$?
  (( rc <= 1 )) || { echo "flrig: could not write xmlport in $f" >&2; return 6; }
  if [[ -z $(dxb_prefs_get "$f" version) ]]; then
    dxb_prefs_set "$f" version "$(_dxb_flrig_version)"; rc=$?
    (( rc <= 1 )) || { echo "flrig: could not write version in $f" >&2; return 6; }
  fi
  return 0
}

# app_flrig_command RADIO INDEX: the command line, one argument per line.
app_flrig_command() {
  printf '%s\n' flrig --config-dir "$(_dxb_flrig_dir "$1")/"
}
