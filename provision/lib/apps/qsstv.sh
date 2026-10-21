#!/bin/bash
# shellcheck shell=bash
# QSSTV as a program on a radio (applications spec; ruling 3: it uses the session's PulseAudio;
# ruling 5: single copy, its settings path is fixed). The settings file is
# ~/.config/ON4QZ/qsstv_9.0.conf (Qt INI). The first fill and the wiring run as dxbapps
# (dxberry-app settings), so the file is written by its owner and nothing here changes ownership
# or writes under DXB_APPS_STATE. The wiring writes only the sound-system and radio1 keys below;
# everything else in the file stays the operator's.
#
# Values read from the QSSTV 9.5.8-6 and hamlib 4.6.2 sources:
#  - radioModel is the text of rigControl::getRadioList (rig/rigcontrol.cpp:330-334): the hamlib
#    model number rightJustified(5,' '), then " ", then the manufacturer, then ",", then the model.
#    The lookup (rigcontrol.cpp:350-352) drops the first 5 characters and then simplifies, so the
#    number must fill exactly five columns. Hamlib model 2 is manufacturer "Hamlib", model
#    "NET rigctl" (hamlib rigs/dummy/netrigctl.c:2824-2825): "    2 Hamlib,NET rigctl", with FOUR
#    leading spaces (the comma makes dxb_ini_qstring quote it).
#  - pttType is stored as the (int) of hamlib's ptt_type_t (config/rigconfig.cpp:81,106):
#    RIG_PTT_NONE = 0 and RIG_PTT_RIG = 1, i.e. CAT (hamlib include/hamlib/rig.h:697-698).
# Expects common.sh, inifile.sh, apps.sh and radio.sh to be sourced.

app_qsstv_kind() { echo radio; }
app_qsstv_session() { echo yes; }
app_qsstv_label() { echo QSSTV; }
app_qsstv_multi() { echo no; }
app_qsstv_audio() { echo pulse; }
app_qsstv_screen() { echo yes; }
app_qsstv_needs_service_restart() { echo no; }
app_qsstv_installed() { dxb_apps_installed qsstv; }
app_qsstv_version() { dxb_apps_version qsstv; }
app_qsstv_unit() { dxb_app_session_unit qsstv "$1"; }
# nothing to undo: the settings file stays for the next time the radio is claimed
app_qsstv_unwire() { return 0; }
# the path does not depend on the radio (single copy)
app_qsstv_settings_file() { printf '%s/.config/ON4QZ/qsstv_9.0.conf\n' "$DXB_APPS_HOME"; }

# _dxb_qsstv_set FILE SECTION KEY VALUE: one key; 0 written or unchanged.
_dxb_qsstv_set() {
  dxb_ini_set "$1" "$2" "$3" "$4"
  (( $? != 6 )) || { echo "qsstv: could not write $3 in $1" >&2; return 6; }
}

# app_qsstv_firstfill RADIO: call sign and locator (when known).
app_qsstv_firstfill() {
  local f st call grid
  f=$(app_qsstv_settings_file "$1")
  st=$(dxb_apps_station) || return 6
  call=$(jq -r .call <<< "$st"); grid=$(jq -r .grid <<< "$st")
  mkdir -p "${f%/*}" || return 6
  _dxb_qsstv_set "$f" PERSONAL callsign "$call" || return 6
  [[ -z $grid ]] || _dxb_qsstv_set "$f" PERSONAL locator "$grid" || return 6
}

# app_qsstv_wire RADIO: PulseAudio sound and rig control through RADIO's rigctld (these keys only).
app_qsstv_wire() {
  local f w pttt=0
  f=$(app_qsstv_settings_file "$1")
  w=$(dxb_apps_wiring "$1") || return 6
  [[ $(jq -r .ptt <<< "$w") != cat ]] || pttt=1
  mkdir -p "${f%/*}" || return 6
  _dxb_qsstv_set "$f" SOUND alsaSelected false || return 6
  _dxb_qsstv_set "$f" SOUND pulseSelected true || return 6
  _dxb_qsstv_set "$f" radio1 enableCAT true || return 6
  _dxb_qsstv_set "$f" radio1 radioModel "$(dxb_ini_qstring "    2 Hamlib,NET rigctl")" || return 6
  _dxb_qsstv_set "$f" radio1 serialPort "$(jq -r .rigctld <<< "$w")" || return 6
  _dxb_qsstv_set "$f" radio1 pttType "$pttt" || return 6
  _dxb_qsstv_set "$f" radio1 enableSerialPTT false || return 6
  _dxb_qsstv_set "$f" radio1 enableXMLRPC false || return 6
}

# app_qsstv_command RADIO INDEX: the command line, one argument per line.
app_qsstv_command() {
  printf '%s\n' qsstv
}
