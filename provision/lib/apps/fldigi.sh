#!/bin/bash
# shellcheck shell=bash
# fldigi as a program on a radio (applications spec; ruling 3: it uses the session's PulseAudio).
# One session per radio, each with its own configuration directory ".fldigi-RADIO" whose
# fldigi_def.xml is the settings file. The first fill, the wiring and the logbook follow run as
# dxbapps (dxberry-app settings), so every file is written by its owner and nothing here changes
# ownership or writes under DXB_APPS_STATE. The wiring writes only the sound, rig control, PTT
# and port keys; everything else in the file stays the operator's.
# Expects common.sh, inifile.sh, apps.sh and radio.sh to be sourced.

app_fldigi_kind() { echo radio; }
app_fldigi_session() { echo yes; }
app_fldigi_label() { echo fldigi; }
app_fldigi_multi() { echo yes; }
app_fldigi_audio() { echo pulse; }
app_fldigi_screen() { echo yes; }
app_fldigi_needs_service_restart() { echo no; }
app_fldigi_installed() { dxb_apps_installed fldigi; }
app_fldigi_version() { dxb_apps_version fldigi; }
app_fldigi_unit() { dxb_app_session_unit fldigi "$1"; }
# nothing to undo: the settings stay for the next time the radio is claimed
app_fldigi_unwire() { return 0; }
_dxb_fldigi_dir() { printf '%s/.fldigi-%s\n' "$DXB_APPS_HOME" "$1"; }
app_fldigi_settings_file() { printf '%s/fldigi_def.xml\n' "$(_dxb_fldigi_dir "$1")"; }
# what DXBerry recorded of its logbook values: sha256 hashes only, never a value
_dxb_fldigi_logbook_record() { printf '%s/dxberry-logbook.json\n' "$(_dxb_fldigi_dir "$1")"; }

# _dxb_fldigi_set FILE KEY VALUE: one key; 0 written or unchanged.
_dxb_fldigi_set() {
  dxb_xml_set "$1" "$2" "$3"
  (( $? != 6 )) || { echo "fldigi: could not write $2 in $1" >&2; return 6; }
}

# _dxb_fldigi_hash VALUE: its sha256 (the value goes in through stdin, never an argument).
_dxb_fldigi_hash() { printf '%s' "$1" | sha256sum | cut -d' ' -f1; }

# _dxb_fldigi_logbook_apply RADIO: write the current logbook's values (dxb_logbook_fldigi_values
# answers {"url","key","station_id"} or {}) into every logbook key whose value still hashes to
# what DXBerry last wrote there (a key never written counts as fldigi's own default). A key the
# operator changed is left alone and reported on stdout as "kept KEY (changed in fldigi)". The
# record keeps only hashes. 0 ok (also with no logbook), 6 could not write.
_dxb_fldigi_logbook_apply() {
  local f rec lb url key sid pair k v cur old
  declare -F dxb_logbook_fldigi_values > /dev/null || return 0
  f=$(app_fldigi_settings_file "$1"); rec=$(_dxb_fldigi_logbook_record "$1")
  [[ -f $f ]] || return 0
  lb=$(dxb_logbook_fldigi_values 2> /dev/null) || return 0
  url=$(jq -r '.url // ""' <<< "$lb" 2> /dev/null); key=$(jq -r '.key // ""' <<< "$lb" 2> /dev/null)
  sid=$(jq -r '.station_id // ""' <<< "$lb" 2> /dev/null)
  [[ -n $url ]] || return 0
  local -A defaults=([ENCLOUDLOG]=0 [CLOUDLOG_API_URL]='' [CLOUDLOG_API_KEY]='' [CLOUDLOG_STATION_ID]=1)
  local -A hashes=()
  for pair in "ENCLOUDLOG=1" "CLOUDLOG_API_URL=$url" "CLOUDLOG_API_KEY=$key" "CLOUDLOG_STATION_ID=${sid:-1}"; do
    k=${pair%%=*}; v=${pair#*=}
    cur=$(dxb_xml_get "$f" "$k") || cur=${defaults[$k]}
    old=$(jq -r --arg k "$k" '.[$k] // ""' "$rec" 2> /dev/null)
    [[ -n $old ]] || old=$(_dxb_fldigi_hash "${defaults[$k]}")
    if [[ $(_dxb_fldigi_hash "$cur") == "$old" ]]; then
      _dxb_fldigi_set "$f" "$k" "$v" || return 6
      hashes[$k]=$(_dxb_fldigi_hash "$v")
    else
      echo "kept $k (changed in fldigi)"
      hashes[$k]=$old
    fi
  done
  local tmp
  tmp=$(umask 077; mktemp "$rec.XXXXXX") || return 6
  jq -n --arg a "${hashes[ENCLOUDLOG]}" --arg b "${hashes[CLOUDLOG_API_URL]}" --arg c "${hashes[CLOUDLOG_API_KEY]}" \
    --arg d "${hashes[CLOUDLOG_STATION_ID]}" \
    '{ENCLOUDLOG: $a, CLOUDLOG_API_URL: $b, CLOUDLOG_API_KEY: $c, CLOUDLOG_STATION_ID: $d}' > "$tmp" \
    || { rm -f "$tmp"; echo "fldigi: could not write $rec" >&2; return 6; }
  if ! { chmod 600 "$tmp" && mv -f "$tmp" "$rec"; }; then
    rm -f "$tmp"; echo "fldigi: could not write $rec" >&2; return 6
  fi
}

# app_fldigi_logbook_follow RADIO: the logbook settings step; the "kept ..." lines go to stdout.
app_fldigi_logbook_follow() { _dxb_fldigi_logbook_apply "$1"; }

# app_fldigi_firstfill RADIO: call sign, locator (when known) and the logbook (when one is set).
app_fldigi_firstfill() {
  local f st call grid
  f=$(app_fldigi_settings_file "$1")
  st=$(dxb_apps_station) || return 6
  call=$(jq -r .call <<< "$st"); grid=$(jq -r .grid <<< "$st")
  mkdir -p "${f%/*}" || return 6
  _dxb_fldigi_set "$f" MYCALL "$call" || return 6
  [[ -z $grid ]] || _dxb_fldigi_set "$f" MYLOC "$grid" || return 6
  _dxb_fldigi_logbook_apply "$1" || return 6
}

# app_fldigi_wire RADIO: the sound, rig control, PTT and ports of RADIO (these keys only, ever).
app_fldigi_wire() {
  local f w i
  f=$(app_fldigi_settings_file "$1")
  w=$(dxb_apps_wiring "$1") || return 6
  i=$(jq -r .index <<< "$w")
  mkdir -p "${f%/*}" || return 6
  _dxb_fldigi_set "$f" AUDIOIO 2 || return 6
  _dxb_fldigi_set "$f" PULSESERVER '' || return 6
  if [[ $(jq -r .control <<< "$w") == flrig ]]; then
    _dxb_fldigi_set "$f" CHKUSEHAMLIBIS 0 || return 6
    _dxb_fldigi_set "$f" FLRIG_AUTO_ENABLE 1 || return 6
    _dxb_fldigi_set "$f" FLRIG_IP_ADDRESS 127.0.0.1 || return 6
    _dxb_fldigi_set "$f" FLRIG_IP_PORT "$(jq -r .flrig_port <<< "$w")" || return 6
  else
    local cmdptt=0
    [[ $(jq -r .ptt <<< "$w") != cat ]] || cmdptt=1
    _dxb_fldigi_set "$f" CHKUSEHAMLIBIS 1 || return 6
    _dxb_fldigi_set "$f" CHKUSEXMLRPCIS 0 || return 6
    _dxb_fldigi_set "$f" FLRIG_AUTO_ENABLE 0 || return 6
    _dxb_fldigi_set "$f" HAMRIGMODEL 2 || return 6
    _dxb_fldigi_set "$f" HAMRIGDEVICE "$(jq -r .rigctld <<< "$w")" || return 6
    _dxb_fldigi_set "$f" HAMLIBCMDPTT "$cmdptt" || return 6
    _dxb_fldigi_set "$f" RTSPTT 0 || return 6
    _dxb_fldigi_set "$f" DTRPTT 0 || return 6
  fi
  _dxb_fldigi_set "$f" XMLRPC_PORT $(( 7362 + i )) || return 6
  _dxb_fldigi_set "$f" ARQ_PORT $(( 7322 + i )) || return 6
}

# app_fldigi_command RADIO INDEX: the command line, one argument per line.
app_fldigi_command() {
  printf '%s\n' fldigi --config-dir "$(_dxb_fldigi_dir "$1")/"
}
