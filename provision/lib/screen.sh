#!/bin/bash
# shellcheck shell=bash
# Program sessions (applications spec section 7): one unit per program per radio,
# dxberry-screen@<app>-<radio>.service, named by its session S = APP-RADIO (APP has no "-"). The
# unit's root steps (dxberry-app present / prepare / stopped), the settings step and the session
# runner (dxberry-app settings / session, as dxbapps) are built from these functions.
#
# Two directories:
# - the screens directory (/run/dxberry/screens, 3770 root:dxbscreen, sticky) holds only the VNC
#   sockets S.sock, made by Xvnc as dxbapps. Root never writes, chmods or chowns anything there.
# - the sessions directory (/run/dxberry/sessions, 0750 root:dxbscreen, root's) holds S.env
#   (0640 root:dxbscreen: what the runner and the program tab read), the S.waiting markers
#   (radio.sh) and each session's private directory S.d (0700 dxbapps: client.conf, the X
#   cookie, the PulseAudio socket, the runtime directory - all written by the session itself).
# The program's settings files are written as dxbapps (dxberry-app settings), never as root.
# Nothing here ever sources an env file: it is read back as data.
# Expects common.sh, radio.sh and apps.sh to be sourced.

: "${DXB_SCREEN_GEOMETRY:=1280x800}"
: "${DXB_SYSTEMCTL:=systemctl}"
: "${DXB_XVNC:=Xvnc}"
: "${DXB_OPENBOX:=openbox}"
: "${DXB_PULSEAUDIO:=pulseaudio}"
: "${DXB_XAUTH:=xauth}"
: "${DXB_ID:=id}"
# where X servers keep their lock files and sockets
: "${DXB_X_LOCK_DIR:=/tmp}"
: "${DXB_X11_UNIX_DIR:=/tmp/.X11-unix}"
# this command, run again as dxbapps for the settings step
: "${DXB_APP_CMD:=/opt/dxberry/bin/dxberry-app}"
# seconds a stopping program gets between TERM and KILL (ruling 9)
: "${DXB_SCREEN_STOP_WAIT_S:=10}"
# seconds prepare waits for the radio's rigctld
: "${DXB_RIGCTLD_WAIT_S:=30}"
# seconds the session waits for Xvnc's X socket
: "${DXB_XVNC_WAIT_S:=30}"
# the env file's keys, in the order they are written
DXB_SCREEN_KEYS='APP RADIO LABEL RADIO_LABEL INDEX DEVICE CARD RIGCTLD AUDIO SCREEN GEOMETRY DISPLAY_NUM'

# dxb_screen_split S: "APP RADIO". 2 when S is not a session name.
dxb_screen_split() {
  [[ $1 =~ ^([a-z][a-z0-9_]{0,15})-([a-z][a-z0-9]{0,11})$ ]] || { dxb_error "not a program session: $1 (PROGRAM-RADIO, e.g. wsjtx-radio2)"; return 2; }
  printf '%s %s\n' "${BASH_REMATCH[1]}" "${BASH_REMATCH[2]}"
}
dxb_screen_sock() { printf '%s/%s.sock\n' "$(_dxb_screens_dir)" "$1"; }
dxb_screen_envfile() { printf '%s/%s.env\n' "$(_dxb_sessions_dir)" "$1"; }
dxb_screen_dir() { printf '%s/%s.d\n' "$(_dxb_sessions_dir)" "$1"; }

# ---- what the module says (the module loaded: dxb_app_load) ----------------------------------
# dxb_screen_audio APP: alsa (the program opens plughw:CARD=<RADIO>,DEV=0 itself) or pulse (it
# gets a private PulseAudio with only that card: ruling 2). Default alsa.
dxb_screen_audio() { local v=alsa; declare -F "app_$1_audio" > /dev/null && v=$("app_$1_audio"); [[ $v == pulse ]] && echo pulse || echo alsa; }
# dxb_screen_wants_screen APP: yes (an X screen) or no (a program with no window, e.g. a web interface).
dxb_screen_wants_screen() { local v=yes; declare -F "app_$1_screen" > /dev/null && v=$("app_$1_screen"); [[ $v == no ]] && echo no || echo yes; }
dxb_screen_label() { local v=''; declare -F "app_$1_label" > /dev/null && v=$("app_$1_label"); printf '%s\n' "${v:-$1}"; }
# dxb_screen_open APP: how the page opens the program, {"kind":"screen"} or {"kind":"url","port":N}.
dxb_screen_open() {
  local v='' def='{"kind":"screen"}'
  declare -F "app_$1_open" > /dev/null && v=$("app_$1_open" | jq -ce 'select(type == "object" and (.kind | type) == "string")' 2> /dev/null)
  printf '%s\n' "${v:-$def}"
}

# dxb_screen_display APP INDEX: the session's fixed X display number: 100 + 2 x the radio's index,
# + 1 for flrig (its rig-control session on the same radio).
dxb_screen_display() { local f=0; [[ $1 == flrig ]] && f=1; echo $(( 100 + 2 * $2 + f )); }

# dxb_screen_env S: the session's env file text (root: it reads the radio record), one KEY=VALUE
# per line in DXB_SCREEN_KEYS order, each value printf %q-quoted. Only S's own radio and card are
# named. 2 bad name, 3 no such program or radio.
dxb_screen_env() {
  local ar a r w i
  ar=$(dxb_screen_split "$1") || return 2
  a=${ar% *}; r=${ar#* }
  dxb_app_load "$a" || return 3
  w=$(dxb_apps_wiring "$r") || return 3
  i=$(jq -r .index <<< "$w")
  printf '%s=%q\n' APP "$a" RADIO "$r" LABEL "$(dxb_screen_label "$a")" RADIO_LABEL "$(jq -r .label <<< "$w")" \
    INDEX "$i" DEVICE "$(jq -r .device <<< "$w")" CARD "$(jq -r .card <<< "$w")" \
    RIGCTLD "$(jq -r .rigctld <<< "$w")" AUDIO "$(dxb_screen_audio "$a")" SCREEN "$(dxb_screen_wants_screen "$a")" \
    GEOMETRY "$DXB_SCREEN_GEOMETRY" DISPLAY_NUM "$(dxb_screen_display "$a" "$i")"
}

# _dxb_screen_unq VALUE: a printf %q value read back as data (never evaluated): $'...' through
# printf %b, '...' without its quotes, anything else without its backslashes.
_dxb_screen_unq() {
  local v=$1 out='' i c
  if [[ $v == "\$'"*"'" ]]; then v=${v:2:${#v}-3}; printf '%b' "${v//\\\'/\'}"; return; fi
  if [[ $v == "'"*"'" && ${#v} -ge 2 ]]; then printf '%s' "${v:1:${#v}-2}"; return; fi
  for (( i = 0; i < ${#v}; i++ )); do
    c=${v:i:1}
    if [[ $c == "\\" ]]; then (( i++ )); c=${v:i:1}; fi
    out+=$c
  done
  printf '%s' "$out"
}

# dxb_screen_parse TEXT: env file TEXT into the array DXB_SCREEN_ENV (KEY -> value), only the
# keys of DXB_SCREEN_KEYS, values as data.
declare -gA DXB_SCREEN_ENV=()
dxb_screen_parse() {
  local line k
  DXB_SCREEN_ENV=()
  while IFS= read -r line; do
    k=${line%%=*}
    [[ $line == *=* && " $DXB_SCREEN_KEYS " == *" $k "* ]] || continue
    DXB_SCREEN_ENV[$k]=$(_dxb_screen_unq "${line#*=}")
  done <<< "$1"
}

# dxb_screen_session_argv S xvnc|openbox|pulse [ENVFILE]: one argument per line, the command
# first: Xvnc on its fixed display with S's X cookie, its VNC on S's unix socket only (no TCP,
# security None: the socket's group and Cockpit's login are the protection), the window manager,
# or a PulseAudio that loads only S's card (a sink and a source named after it) and a native
# socket in S.d. The values come from ENVFILE (the runner, which cannot read the radio record) or
# else from dxb_screen_env S. Pure: it starts nothing. 2 bad name or list, 3 unknown, 6 ENVFILE
# unreadable.
dxb_screen_session_argv() {
  local s=$1 what=$2 text sock dir dev card
  dxb_screen_split "$s" > /dev/null || return 2
  if [[ -n ${3:-} ]]; then text=$(< "$3") || return 6; else text=$(dxb_screen_env "$s") || return $?; fi
  dxb_screen_parse "$text"
  sock=$(dxb_screen_sock "$s"); dir=$(dxb_screen_dir "$s")
  dev=${DXB_SCREEN_ENV[DEVICE]:-}; card=${DXB_SCREEN_ENV[CARD]:-}
  case $what in
    xvnc)
      printf '%s\n' "$DXB_XVNC" ":${DXB_SCREEN_ENV[DISPLAY_NUM]:-}" -auth "$dir/Xauthority" \
        -rfbunixpath "$sock" -rfbunixmode 0660 -rfbport -1 -SecurityTypes None \
        -geometry "${DXB_SCREEN_ENV[GEOMETRY]:-$DXB_SCREEN_GEOMETRY}" -nolisten tcp \
        -desktop "${DXB_SCREEN_ENV[LABEL]:-} · ${DXB_SCREEN_ENV[RADIO_LABEL]:-}" ;;
    openbox) printf '%s\n' "$DXB_OPENBOX" --sm-disable ;;
    pulse)
      printf '%s\n' "$DXB_PULSEAUDIO" -n --daemonize=no --exit-idle-time=-1 --use-pid-file=no -F /dev/null \
        -L "module-alsa-sink device=$dev sink_name=$card" -L "module-alsa-source device=$dev source_name=$card" \
        -L "module-native-protocol-unix socket=$dir/pulse.sock" ;;
    *) dxb_error "dxb_screen_session_argv: no argument list $what"; return 2 ;;
  esac
}

# ---- the unit's root steps -------------------------------------------------------------------
# _dxb_screen_put FILE CONTENT MODE OWNER: FILE (in the root-only sessions directory) made new
# with mktemp and renamed over whatever was there. 0 ok, 1 failed.
_dxb_screen_put() {
  local f=$1 tmp
  tmp=$(mktemp "${f%/*}/.dxbtmp.XXXXXXXX") || return 1
  if printf '%s\n' "$2" > "$tmp" && chmod "$3" "$tmp" && "$DXB_CHOWN" "$4" "$tmp" && mv -fT "$tmp" "$f"; then return 0; fi
  rm -f "$tmp"
  return 1
}

# dxb_screen_mark_waiting S: S waits for its radio (radio.sh's apply starts it when the radio is
# back; nothing for a rig-control session). Always 0: a marker that cannot be written is a warning.
dxb_screen_mark_waiting() {
  local f; f=$(_dxb_sessions_dir)/$1.waiting
  # a rig-control session (flrig) is started and stopped by apply itself, never marked as waiting
  dxb_app_load "${1%%-*}" > /dev/null 2>&1 && [[ $(dxb_app_kind "${1%%-*}") == rigcontrol ]] && return 0
  { _dxb_sessions_ensure && _dxb_screen_put "$f" '' 600 root:root; } 2> /dev/null \
    || dxb_warn "could not write $f; start $1 by hand when its radio is back"
  return 0
}

# _dxb_screen_wait_rigctld RADIO WIRING: when the program talks to the radio through rigctld (it
# has CAT, or rigctld keys its PTT), wait up to DXB_RIGCTLD_WAIT_S for rigctld@RADIO; the
# program starts anyway after that (it shows its own rig error). Always 0.
_dxb_screen_wait_rigctld() {
  local u="rigctld@$1.service" i
  [[ $(jq -r --arg n "$1" '.radios[$n].cat != null' <<< "$DXB_RADIOS") == true || $(jq -r .ptt <<< "$2") == cat ]] || return 0
  for (( i = 0; ; i++ )); do
    "$DXB_SYSTEMCTL" is-active --quiet "$u" && return 0
    (( i >= DXB_RIGCTLD_WAIT_S )) && break
    command sleep 1
  done
  dxb_warn "$u is not active after ${DXB_RIGCTLD_WAIT_S}s; starting the program anyway"
  return 0
}

# _dxb_screen_logbook: the logbook a program is told about ({"url","key","station_id"} or {}),
# from logbook.sh when it is loaded.
_dxb_screen_logbook() {
  local j='' none='{}'
  declare -F dxb_logbook_fldigi_values > /dev/null && j=$(dxb_logbook_fldigi_values 2> /dev/null | jq -ce 'select(type == "object")' 2> /dev/null)
  printf '%s\n' "${j:-$none}"
}

# dxb_screen_settings_input S STEP...: the one JSON object the settings step reads on stdin
# (root builds it): only S's radio from the record, the station keys, the logbook, and the steps
# to run. The logbook (its API key) reaches jq on stdin: only data that is no secret is ever in
# an argument, where any user could read it in /proc.
dxb_screen_settings_input() {
  local s=$1 r; shift
  r=${s#*-}
  _dxb_screen_logbook | jq -c --argjson rec "$(jq -c --arg r "$r" '{version, gps, radios: {($r): .radios[$r]}}' <<< "$DXB_RADIOS")" \
    --arg c "${DXB_CFG[CALLSIGN]:-}" --arg la "${DXB_CFG[LATITUDE]:-}" --arg lo "${DXB_CFG[LONGITUDE]:-}" \
    '{radios: $rec, cfg: {CALLSIGN: $c, LATITUDE: $la, LONGITUDE: $lo}, logbook: ., steps: $ARGS.positional}' --args "$@"
}

# _dxb_screen_run_settings S STEP...: the settings step for S as dxbapps (dxberry-app settings S
# through runuser), its input built here (dxb_screen_settings_input) and handed over on stdin.
# Its stdout ("done STEP" and the logbook step's "kept" lines) on stdout; its exit status.
_dxb_screen_run_settings() {
  local s=$1; shift
  dxb_screen_settings_input "$s" "$@" \
    | "$DXB_RUNUSER" -u "$DXB_APPS_USER" -- env HOME="$DXB_APPS_HOME" "$DXB_APP_CMD" settings "$s"
}

# dxb_screen_kept_file S: where root keeps S's last "kept KEY (changed in PROGRAM)" lines (the
# logbook keys the operator changed in the program, which the logbook no longer sets there), for
# the Programs row. Root's own state directory; 0600.
dxb_screen_kept_file() { printf '%s/logbook-kept/%s\n' "$DXB_APPS_STATE" "$1"; }

# _dxb_screen_kept_store S OUT: after a logbook step that reported done (OUT, the settings step's
# stdout), its "kept" lines replace S's kept file; none removes it. Always 0 (a warning).
_dxb_screen_kept_store() {
  local f k
  grep -qx 'done logbook' <<< "$2" || return 0
  f=$(dxb_screen_kept_file "$1"); k=$(grep '^kept ' <<< "$2")
  if [[ -z $k ]]; then rm -f "$f"; return 0; fi
  { mkdir -p "${f%/*}" && { dxb_write_if_changed "$f" "$k" 600; [[ $(< "$f") == "$k" ]]; }; } 2> /dev/null \
    || dxb_warn "$1: could not record the logbook keys kept in the program ($f)"
  return 0
}

# dxb_screen_logbook_follow S: the logbook step alone for S (a stopped session follows a new
# logbook now: dxberry-logbook set). Its "kept" lines on stdout and in S's kept file. DXB_RADIOS
# loaded. 0 ok, 7 the step failed (a warning).
dxb_screen_logbook_follow() {
  local out rc
  out=$(_dxb_screen_run_settings "$1" logbook); rc=$?
  _dxb_screen_kept_store "$1" "$out"
  grep '^kept ' <<< "$out"
  (( rc == 0 )) || { dxb_warn "$1: could not follow the logbook (exit $rc); it does at its next start"; return 7; }
  return 0
}

# dxb_screen_settings S: the settings step itself, as dxbapps: one JSON object
# (dxb_screen_settings_input) on stdin; DXB_RADIOS and DXB_CFG come from it alone, and
# dxb_logbook_fldigi_values answers its logbook. Runs app_APP_firstfill and / or app_APP_wire
# RADIO in the order given (a module without the hook has nothing to do) and prints "done STEP"
# after each one; the module's own output goes to stderr. The step "logbook" calls
# app_APP_logbook_follow RADIO instead, whose stdout (the "kept KEY (changed in PROGRAM)" lines)
# passes through before "done logbook". 0 ok, 2 bad input, 3 unknown program
# or radio, 7 a step failed.
DXB_APPS_LOGBOOK='{}'
dxb_screen_settings() {
  local s=$1 ar a r in step
  ar=$(dxb_screen_split "$s") || return 2
  a=${ar% *}; r=${ar#* }
  in=$(cat)
  jq -e 'type == "object" and (.radios | type) == "object" and (.steps | type) == "array"' <<< "$in" > /dev/null 2>&1 \
    || { echo "the settings step reads one JSON object on stdin" >&2; return 2; }
  DXB_RADIOS=$(jq -c '.radios' <<< "$in")
  dxb_radio_get "$r" > /dev/null || return 3
  DXB_CFG=()
  DXB_CFG[CALLSIGN]=$(jq -r '.cfg.CALLSIGN // ""' <<< "$in")
  DXB_CFG[LATITUDE]=$(jq -r '.cfg.LATITUDE // ""' <<< "$in")
  DXB_CFG[LONGITUDE]=$(jq -r '.cfg.LONGITUDE // ""' <<< "$in")
  DXB_CFG[_GPS]=1; DXB_CFG[GPS_DEVICE]=auto; DXB_CFG[GPS_PPS]=''; DXB_CFG[_GPS_PATH]=''; DXB_CFG[GPS_BAUD]=9600
  DXB_APPS_LOGBOOK=$(jq -c '.logbook // {} | if type == "object" then . else {} end' <<< "$in")
  # shellcheck disable=SC2317,SC2329  # called by the modules (fldigi's first fill), not here
  dxb_logbook_fldigi_values() { printf '%s\n' "$DXB_APPS_LOGBOOK"; }
  dxb_app_load "$a" || return 3
  for step in $(jq -r '.steps[]' <<< "$in"); do
    case $step in
      firstfill|wire|logbook) ;;
      *) echo "unknown settings step: $step" >&2; return 2 ;;
    esac
    if [[ $step == logbook ]]; then
      # the logbook step reports on stdout (the "kept KEY (changed in PROGRAM)" lines); its
      # other output is stderr as for every step
      if declare -F "app_${a}_logbook_follow" > /dev/null; then
        "app_${a}_logbook_follow" "$r" || { echo "$s: $step failed" >&2; return 7; }
      fi
    elif declare -F "app_${a}_$step" > /dev/null; then
      "app_${a}_$step" "$r" >&2 || { echo "$s: $step failed" >&2; return 7; }
    fi
    echo "done $step"
  done
  return 0
}

# _dxb_screen_companions APP RADIO: the units that run beside APP's session, one per line - the
# module's optional app_APP_companions RADIO (a helper the program needs, e.g. Pat's ARDOP modem,
# kept in its own unit so it can be confined). Started by prepare once S.env exists, stopped by
# stopped. Nothing when the module has none.
_dxb_screen_companions() { if declare -F "app_$1_companions" > /dev/null; then "app_$1_companions" "$2"; fi; }

# _dxb_screen_session_ok APP RADIO: APP may run a session on RADIO - it owns the radio and is a
# program that can, or it is a rig-control program (flrig) and RADIO's rig.control names it, whoever
# the owner is. APP is loaded and installed afterwards. 0 yes, 3 no (the reason on stderr).
_dxb_screen_session_ok() {
  local a=$1 r=$2
  if dxb_app_load "$a" 2> /dev/null && [[ $(dxb_app_kind "$a") == rigcontrol ]]; then
    [[ $(jq -r --arg n "$r" '.radios[$n].rig.control // "hamlib"' <<< "$DXB_RADIOS") == "$a" ]] \
      || { dxb_error "radio $r's rig control is not $a"; return 3; }
    dxb_app_is_installed "$a" || { dxb_error "$a is not installed; install it from Programs"; return 3; }
    return 0
  fi
  [[ $(jq -r --arg n "$r" '.radios[$n].owner' <<< "$DXB_RADIOS") == "$a" ]] || { dxb_error "radio $r is not $a's"; return 3; }
  dxb_app_load_owner "$a"
}

# dxb_screen_prepare S: the unit's ExecStartPre (root; DXB_RADIOS loaded, DXB_RADIO_SCAN fresh,
# DXB_CFG loaded for the first fill). RADIO must exist, be owned by APP (or, for a rig-control program
# such as flrig, name it in its rig.control), APP an installed session program, RADIO plugged in. Then the settings step as dxbapps (dxberry-app settings, input on
# stdin): the first fill once per program per radio, and the wiring whenever the radio's wiring
# hash differs from the one last wired for S (ruling 7), or at every start when the module's
# app_APP_wire_each_start says yes (flrig: the operator can switch transceivers inside it); a radio
# set to "names" wiring is never wired; and, at every start, the logbook step for a program that
# follows the logbook (app_APP_logbook_follow: fldigi), whose "kept" lines root keeps in S's kept
# file (dxb_screen_kept_file) - a logbook step that fails is only a warning. Root records each
# step the settings step reports done. Then S.env, a fresh S.d (root
# only makes it and gives it to dxbapps) and the wait for rigctld. 0 ok, 2 bad name, 3 not owned /
# not installed / unknown, 4 radio absent, 6 the session's files could not be made, 7 first fill
# or wiring failed.
dxb_screen_prepare() {
  local s=$1 ar a r w h hf env dir out u rc=0
  local -a steps=()
  ar=$(dxb_screen_split "$s") || return 2
  a=${ar% *}; r=${ar#* }
  dxb_radio_get "$r" > /dev/null || return 3
  _dxb_screen_session_ok "$a" "$r" || return 3
  dxb_app_is_session "$a" || { dxb_error "$a does not run in a screen session"; return 3; }
  dxb_radio_present "$r" || { dxb_error "radio $r is not plugged in"; return 4; }
  w=$(dxb_apps_wiring "$r") || return 3
  h=$(jq -r .hash <<< "$w"); hf=$DXB_APPS_STATE/wired/$s
  dxb_apps_firstfill_done "$a" "$r" || steps+=(firstfill)
  if [[ $(jq -r --arg n "$r" '.radios[$n].wiring' <<< "$DXB_RADIOS") != names ]]; then
    if [[ $(cat "$hf" 2> /dev/null) != "$h" ]] || { declare -F "app_${a}_wire_each_start" > /dev/null && [[ $("app_${a}_wire_each_start") == yes ]]; }; then
      steps+=(wire)
    fi
  fi
  # the module is loaded (_dxb_screen_session_ok): a program that follows the logbook does so at every start
  declare -F "app_${a}_logbook_follow" > /dev/null && steps+=(logbook)
  if (( ${#steps[@]} )); then
    out=$(_dxb_screen_run_settings "$s" "${steps[@]}"); rc=$?
    if grep -qx 'done firstfill' <<< "$out"; then
      dxb_apps_firstfill_mark "$a" "$r" || dxb_warn "$s: could not mark the first fill done; it runs again next time"
    fi
    if grep -qx 'done wire' <<< "$out"; then
      { mkdir -p "${hf%/*}" && printf '%s\n' "$h" > "$hf"; } 2> /dev/null \
        || dxb_warn "$s: could not record the wiring; it is wired again next time"
    fi
    _dxb_screen_kept_store "$s" "$out"
    if (( rc != 0 )); then
      local st ok=1
      for st in "${steps[@]}"; do [[ $st == logbook ]] || grep -qx "done $st" <<< "$out" || ok=0; done
      # only the logbook follow failed: the program starts with the logbook it had
      (( ok )) || { dxb_error "$s: $a's settings could not be written (${steps[*]}; exit $rc)"; return 7; }
      dxb_warn "$s: $a could not follow the logbook (exit $rc); it tries again at the next start"
    fi
  fi
  env=$(dxb_screen_env "$s") || return 3
  dir=$(dxb_screen_dir "$s")
  _dxb_sessions_ensure || { dxb_error "could not create $(_dxb_sessions_dir)"; return 6; }
  _dxb_screen_put "$(dxb_screen_envfile "$s")" "$env" 640 "root:$DXB_APPS_SCREEN_GROUP" \
    || { dxb_error "could not write $(dxb_screen_envfile "$s")"; return 6; }
  rm -rf "$dir"
  if ! { mkdir -m 700 "$dir" && "$DXB_CHOWN" -h "$DXB_APPS_USER:$DXB_APPS_USER" "$dir"; }; then
    dxb_error "could not prepare $dir"; return 6
  fi
  # the program's companion units (app_APP_companions RADIO, optional) start now that S.env exists;
  # one that does not start is a warning, not a failed session
  for u in $(_dxb_screen_companions "$a" "$r"); do
    "$DXB_SYSTEMCTL" start "$u" || dxb_warn "$s: could not start $u"
  done
  # rigctld (model 4) depends on flrig, not the other way round: flrig never waits for it
  [[ $(dxb_app_kind "$a") == rigcontrol ]] || _dxb_screen_wait_rigctld "$r" "$w"
  return 0
}

# dxb_screen_stopped S: after the session ended (its ExecStopPost, root). A radio that is gone
# right now leaves S waiting for it, so a program that ended because its card vanished comes back
# on replug; anything else (quit from its menu, Stop) leaves nothing. S.d goes; the screen socket
# is the session's own (Xvnc removes it). Always 0.
dxb_screen_stopped() {
  local ar r u
  ar=$(dxb_screen_split "$1" 2> /dev/null) || return 0
  r=${ar#* }
  dxb_app_load "${ar% *}" > /dev/null 2>&1
  for u in $(_dxb_screen_companions "${ar% *}" "$r"); do "$DXB_SYSTEMCTL" stop "$u" || dxb_warn "$1: could not stop $u"; done
  rm -rf "$(dxb_screen_dir "$1")"
  dxb_radio_get "$r" > /dev/null 2>&1 || return 0
  dxb_radio_present "$r" || dxb_screen_mark_waiting "$1"
  return 0
}

# dxb_screen_unit_state UNIT: running | starting | stopped | failed.
dxb_screen_unit_state() {
  case $("$DXB_SYSTEMCTL" is-active "$1" 2> /dev/null) in
    active|reloading) echo running ;;
    activating) echo starting ;;
    failed) echo failed ;;
    *) if "$DXB_SYSTEMCTL" is-failed --quiet "$1" 2> /dev/null; then echo failed; else echo stopped; fi ;;
  esac
}
# dxb_screen_state S: dxb_screen_unit_state of S's unit.
dxb_screen_state() { dxb_screen_unit_state "dxberry-screen@$1.service"; }

# dxb_screen_access: 0 when the caller may open screen sockets (root, or in the dxbscreen group
# right now - a login made before the user joined it is not).
dxb_screen_access() {
  (( ${DXB_FAKE_EUID:-$EUID} == 0 )) && return 0
  [[ " $("$DXB_ID" -nG 2> /dev/null) " == *" $DXB_APPS_SCREEN_GROUP "* ]]
}

# ---- the session runner (as dxbapps) ---------------------------------------------------------
_DXB_SCREEN_TERM=0

# _dxb_screen_end PID...: TERM each still running PID, then KILL what is left after 5 s.
_dxb_screen_end() {
  local p i
  for p in "$@"; do [[ -n $p ]] && kill -TERM "$p" 2> /dev/null; done
  for (( i = 0; i < 50; i++ )); do
    for p in "$@"; do [[ -n $p ]] && kill -0 "$p" 2> /dev/null && break; p=''; done
    [[ -z $p ]] && break
    command sleep 0.1
  done
  for p in "$@"; do [[ -n $p ]] && kill -KILL "$p" 2> /dev/null; done
  for p in "$@"; do [[ -n $p ]] && wait "$p" 2> /dev/null; done
  return 0
}

# _dxb_screen_free_display N: display N may be started: a lock /tmp/.XN-lock whose process is gone
# is removed (with its socket) when it is this user's own; one whose process lives, or another
# user's stale one, is refused. 0 free, 1 not (the reason on stderr).
_dxb_screen_free_display() {
  local n=$1 lock xs pid=''
  lock=$DXB_X_LOCK_DIR/.X$n-lock; xs=$DXB_X11_UNIX_DIR/X$n
  if [[ -e $lock || -L $lock ]]; then
    # what it is, before anything reads it: a symlink, a FIFO or another user's file is never opened
    if [[ -L $lock || ! -f $lock ]]; then
      echo "display :$n: $lock is not a lock file; remove it (sudo rm $lock)" >&2; return 1
    fi
    if [[ ! -O $lock ]]; then
      echo "display :$n is locked by another user ($lock); if no X server runs there, remove it (sudo rm $lock $xs)" >&2; return 1
    fi
    read -r pid < "$lock" 2> /dev/null
    pid=${pid//[[:space:]]/}
    if [[ $pid =~ ^[0-9]+$ && -e /proc/$pid ]]; then
      echo "display :$n is in use (process $pid, $lock)" >&2; return 1
    fi
    rm -f "$lock" || { echo "could not remove the stale $lock" >&2; return 1; }
  fi
  if [[ -e $xs || -L $xs ]]; then
    [[ -O $xs && ! -L $xs ]] || { echo "$xs belongs to another user; remove it (sudo rm $xs)" >&2; return 1; }
    rm -f "$xs"
  fi
  return 0
}

# _dxb_screen_x_ready XS PID: Xvnc (PID) runs and its X socket XS is there: a socket (not a
# symlink) of this user's own. DXB_X_SOCKET_TYPE=file accepts a plain file instead (only the
# tests: they cannot make a unix socket).
_dxb_screen_x_ready() {
  kill -0 "$2" 2> /dev/null || return 1
  [[ -O $1 && ! -L $1 ]] || return 1
  if [[ ${DXB_X_SOCKET_TYPE:-socket} == file ]]; then [[ -f $1 ]]; else [[ -S $1 ]]; fi
}

# _dxb_screen_cookie FILE N: a new X authority FILE for display :N with a random cookie. The
# cookie is in xauth's own argument list, a process of this same user (dxbapps) - nobody else's.
_dxb_screen_cookie() {
  local c
  c=$(od -An -N16 -tx1 /dev/urandom | tr -d ' \n')
  [[ $c =~ ^[0-9a-f]{32}$ ]] || return 1
  rm -f "$1"
  ( umask 077; "$DXB_XAUTH" -q -f "$1" add ":$2" MIT-MAGIC-COOKIE-1 "$c" )
}

# dxb_screen_session_run S: the session itself. Reads S.env (never the radio record), writes
# client.conf (no sound server may spawn) and the X cookie into S.d, starts Xvnc on the session's
# fixed display (SCREEN=yes; ready once it runs and its X socket is a socket of this user's own)
# and openbox, the private PulseAudio
# (AUDIO=pulse), then the program (app_APP_command RADIO INDEX, one argument per line) with
# HOME, DISPLAY, XAUTHORITY, XDG_RUNTIME_DIR=S.d and PULSE_SERVER / PULSE_CLIENTCONFIG set for
# every program (an ALSA one too). The program also sees APP, RADIO, INDEX, DEVICE, CARD, RIGCTLD
# and DXB_APPS_OPT. Ends with the program's own exit status; on TERM the program gets TERM, then
# KILL after DXB_SCREEN_STOP_WAIT_S (ruling 9), and the session ends 0 (a stop is not a failure).
# Xvnc and PulseAudio ending first end the program too (status 1); the helpers always end with
# the program.
dxb_screen_session_run() {
  local s=$1 envf dir sock n xs i rc=0 done_pid a r
  local xpid='' opid='' ppid='' prog='' why=''
  local -a argv=() xv=() ob=() pa=() live=()
  envf=$(dxb_screen_envfile "$s"); dir=$(dxb_screen_dir "$s"); sock=$(dxb_screen_sock "$s")
  [[ -f $envf && -r $envf && -d $dir ]] || { echo "$envf or $dir is missing: the session's prepare step did not run" >&2; return 6; }
  dxb_screen_parse "$(< "$envf")"
  a=${DXB_SCREEN_ENV[APP]:-}; r=${DXB_SCREEN_ENV[RADIO]:-}
  [[ "$a-$r" == "$s" ]] || { echo "$envf is not $s's" >&2; return 6; }
  dxb_app_load "$a" || return 3
  declare -F "app_${a}_command" > /dev/null || { echo "$a has no command to run" >&2; return 6; }
  mapfile -t argv < <("app_${a}_command" "$r" "${DXB_SCREEN_ENV[INDEX]:-0}")
  [[ ${#argv[@]} -gt 0 && -n ${argv[0]} ]] || { echo "$a gave no command to run" >&2; return 6; }
  printf 'autospawn = no\n' > "$dir/client.conf" || { echo "could not write $dir/client.conf" >&2; return 6; }
  export HOME=$DXB_APPS_HOME XDG_RUNTIME_DIR=$dir PULSE_SERVER="unix:$dir/pulse.sock" PULSE_CLIENTCONFIG="$dir/client.conf" \
    APP=$a RADIO=$r INDEX=${DXB_SCREEN_ENV[INDEX]:-} DEVICE=${DXB_SCREEN_ENV[DEVICE]:-} CARD=${DXB_SCREEN_ENV[CARD]:-} \
    RIGCTLD=${DXB_SCREEN_ENV[RIGCTLD]:-} DXB_APPS_OPT
  unset DISPLAY XAUTHORITY
  _DXB_SCREEN_TERM=0
  trap '_DXB_SCREEN_TERM=1' TERM INT HUP
  if [[ ${DXB_SCREEN_ENV[SCREEN]:-yes} == yes ]]; then
    n=${DXB_SCREEN_ENV[DISPLAY_NUM]:-}
    [[ $n =~ ^[0-9]+$ ]] || { echo "$envf names no display" >&2; return 6; }
    _dxb_screen_free_display "$n" || return 6
    _dxb_screen_cookie "$dir/Xauthority" "$n" || { echo "could not write the X cookie $dir/Xauthority" >&2; return 6; }
    mapfile -t xv < <(dxb_screen_session_argv "$s" xvnc "$envf")
    mapfile -t ob < <(dxb_screen_session_argv "$s" openbox "$envf")
    # the session's own socket from an earlier run (the directory is sticky: only it may remove it)
    rm -f "$sock"
    xs=$DXB_X11_UNIX_DIR/X$n
    "${xv[@]}" &
    xpid=$!
    for (( i = 0; i < DXB_XVNC_WAIT_S * 10; i++ )); do
      _dxb_screen_x_ready "$xs" "$xpid" && break
      kill -0 "$xpid" 2> /dev/null || break
      (( _DXB_SCREEN_TERM )) && break
      command sleep 0.1
    done
    if ! _dxb_screen_x_ready "$xs" "$xpid"; then
      (( _DXB_SCREEN_TERM )) || echo "Xvnc did not start for $s (display :$n)" >&2
      _dxb_screen_end "$xpid"
      (( _DXB_SCREEN_TERM )) && return 0
      return 6
    fi
    export DISPLAY=":$n" XAUTHORITY="$dir/Xauthority"
    "${ob[@]}" &
    opid=$!
  fi
  if [[ ${DXB_SCREEN_ENV[AUDIO]:-alsa} == pulse && $_DXB_SCREEN_TERM == 0 ]]; then
    mapfile -t pa < <(dxb_screen_session_argv "$s" pulse "$envf")
    "${pa[@]}" &
    ppid=$!
    for (( i = 0; i < 100; i++ )); do
      [[ -S $dir/pulse.sock ]] && break
      kill -0 "$ppid" 2> /dev/null || break
      (( _DXB_SCREEN_TERM )) && break
      command sleep 0.1
    done
    if [[ ! -S $dir/pulse.sock && $_DXB_SCREEN_TERM == 0 ]]; then
      echo "PulseAudio did not start for $s" >&2
      _dxb_screen_end "$ppid" "$opid" "$xpid"
      return 6
    fi
  fi
  if (( _DXB_SCREEN_TERM )); then _dxb_screen_end "$ppid" "$opid" "$xpid"; return 0; fi
  "${argv[@]}" &
  prog=$!
  live=("$prog")
  for i in "$xpid" "$ppid" "$opid"; do [[ -z $i ]] || live+=("$i"); done
  while :; do
    # checked before every wait: a TERM that came while the last round was being handled is not lost
    (( _DXB_SCREEN_TERM )) && { why=term; break; }
    done_pid=''
    wait -n -p done_pid "${live[@]}"; rc=$?
    (( _DXB_SCREEN_TERM )) && { why=term; break; }
    if [[ -z $done_pid ]]; then
      # 127: none of them is a child any more (cannot happen while the program runs; never spin)
      (( rc == 127 )) && { echo "lost track of $a's processes; ending the session" >&2; why=helper; break; }
      continue
    fi
    if [[ $done_pid == "$prog" ]]; then prog=''; why=program; break; fi
    if [[ $done_pid == "$opid" ]]; then
      # a window manager that ended leaves the program usable
      echo "openbox ended (exit $rc); $a keeps running" >&2
      opid=''; live=("$prog")
      for i in "$xpid" "$ppid"; do [[ -z $i ]] || live+=("$i"); done
      continue
    fi
    [[ $done_pid == "$xpid" ]] && { echo "Xvnc ended (exit $rc); stopping $a" >&2; xpid=''; }
    [[ $done_pid == "$ppid" ]] && { echo "PulseAudio ended (exit $rc); stopping $a" >&2; ppid=''; }
    why=helper
    break
  done
  if [[ -n $prog ]]; then
    kill -TERM "$prog" 2> /dev/null
    for (( i = 0; i < DXB_SCREEN_STOP_WAIT_S * 10; i++ )); do
      kill -0 "$prog" 2> /dev/null || break
      command sleep 0.1
    done
    if kill -0 "$prog" 2> /dev/null; then
      echo "$a did not end within ${DXB_SCREEN_STOP_WAIT_S}s of TERM; killing it" >&2
      kill -KILL "$prog" 2> /dev/null
    fi
    wait "$prog" 2> /dev/null
  fi
  _dxb_screen_end "$opid" "$ppid" "$xpid"
  rm -f "$sock"
  trap - TERM INT HUP
  case $why in
    term) return 0 ;;
    helper) return 1 ;;
  esac
  return $rc
}
