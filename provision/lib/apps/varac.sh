#!/bin/bash
# shellcheck shell=bash
app_varac_kind() { echo radio; }
app_varac_session() { echo yes; }
app_varac_label() { echo VarAC; }
app_varac_multi() { echo no; }
app_varac_audio() { echo pulse; }
app_varac_screen() { echo yes; }
app_varac_needs_service_restart() { echo no; }
app_varac_installed() { dxb_apps_installed varac; }
app_varac_version() { dxb_apps_version varac; }
app_varac_unit() { dxb_app_session_unit varac "$1"; }
app_varac_wire() { return 0; }   # PulseAudio is supplied by the session.
app_varac_unwire() { return 0; }
app_varac_local_version() { python3 "$DXB_LIB/native-archive.py" varac "$1"; }
app_varac_unpack() { python3 "$DXB_LIB/native-archive.py" varac "$1" "$2"; }
app_varac_firstfill() {
  dxb_app_load xara && app_xara_firstfill "$1"
}
app_varac_command() { printf '%s\n' /opt/dxberry/bin/dxberry-varac "$1"; }
app_varac_setup_note() { echo "VarAC 17.1.7 needs a separate modem reporting VARA HF 4.9.1 or newer. Install or update XARA to use the bundled VarAC 17 build. See the README for selecting XARA as the local modem."; }
