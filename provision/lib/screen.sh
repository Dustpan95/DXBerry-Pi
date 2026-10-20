#!/bin/bash
# shellcheck shell=bash
# Program sessions (applications spec section 7): one unit per program per radio,
# dxberry-screen@<app>-<radio>.service, named by its session S = APP-RADIO (APP has no "-"). The
# unit's root steps (dxberry-app present / prepare / stopped) and the session runner
# (dxberry-app session, as dxbapps) are built from these functions. Every file of a session lives
# in the screens directory (/run/dxberry/screens, 2770 root:dxbscreen): S.sock (the VNC socket),
# S.env (0640 root:dxbscreen, what the runner and the program tab read), S.d/ (0700 dxbapps: the
# private PulseAudio socket, client.conf, the runtime directory) and S.waiting (radio.sh).
# Expects common.sh, radio.sh and apps.sh to be sourced.
#
# The screens directory is writable by its group (dxbapps, the console user), so nothing here
# ever sources an env file, and root never writes through a name a group member could have
# planted: new files are made with mktemp and renamed into place.

: "${DXB_SCREEN_GEOMETRY:=1280x800}"
: "${DXB_XVNC:=Xvnc}"
: "${DXB_OPENBOX:=openbox}"
: "${DXB_PULSEAUDIO:=pulseaudio}"
# seconds a stopping program gets between TERM and KILL (ruling 9)
: "${DXB_SCREEN_STOP_WAIT_S:=10}"
# seconds prepare waits for the radio's rigctld
: "${DXB_RIGCTLD_WAIT_S:=30}"
# the env file's keys, in the order they are written
DXB_SCREEN_KEYS='APP RADIO LABEL RADIO_LABEL INDEX DEVICE CARD RIGCTLD AUDIO SCREEN GEOMETRY'

# dxb_screen_split S: "APP RADIO". 2 when S is not a session name.
dxb_screen_split() {
  [[ $1 =~ ^([a-z][a-z0-9_]{0,15})-([a-z][a-z0-9]{0,11})$ ]] || { dxb_error "not a program session: $1 (PROGRAM-RADIO, e.g. wsjtx-radio2)"; return 2; }
  printf '%s %s\n' "${BASH_REMATCH[1]}" "${BASH_REMATCH[2]}"
}
dxb_screen_sock() { printf '%s/%s.sock\n' "$(_dxb_screens_dir)" "$1"; }
dxb_screen_envfile() { printf '%s/%s.env\n' "$(_dxb_screens_dir)" "$1"; }
dxb_screen_dir() { printf '%s/%s.d\n' "$(_dxb_screens_dir)" "$1"; }

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

# dxb_screen_env S: the session's env file text (root: it reads the radio record), one KEY=VALUE
# per line in DXB_SCREEN_KEYS order, each value printf %q-quoted. Only S's own radio and card are
# named. 2 bad name, 3 no such program or radio.
dxb_screen_env() {
  local ar a r w
  ar=$(dxb_screen_split "$1") || return 2
  a=${ar% *}; r=${ar#* }
  dxb_app_load "$a" || return 3
  w=$(dxb_apps_wiring "$r") || return 3
  printf '%s=%q\n' APP "$a" RADIO "$r" LABEL "$(dxb_screen_label "$a")" RADIO_LABEL "$(jq -r .label <<< "$w")" \
    INDEX "$(jq -r .index <<< "$w")" DEVICE "$(jq -r .device <<< "$w")" CARD "$(jq -r .card <<< "$w")" \
    RIGCTLD "$(jq -r .rigctld <<< "$w")" AUDIO "$(dxb_screen_audio "$a")" SCREEN "$(dxb_screen_wants_screen "$a")" \
    GEOMETRY "$DXB_SCREEN_GEOMETRY"
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
# first: Xvnc on S's unix socket only (no TCP, security None: the socket's group and Cockpit's
# login are the protection), the window manager, or a PulseAudio that loads only S's card (a sink
# and a source named after it) and a native socket in S.d. The values come from ENVFILE (the
# runner, which cannot read the radio record) or else from dxb_screen_env S. Pure: it starts
# nothing. 2 bad name or list, 3 unknown, 6 ENVFILE unreadable.
dxb_screen_session_argv() {
  local s=$1 what=$2 text sock dir dev card
  dxb_screen_split "$s" > /dev/null || return 2
  if [[ -n ${3:-} ]]; then text=$(< "$3") || return 6; else text=$(dxb_screen_env "$s") || return $?; fi
  dxb_screen_parse "$text"
  sock=$(dxb_screen_sock "$s"); dir=$(dxb_screen_dir "$s")
  dev=${DXB_SCREEN_ENV[DEVICE]:-}; card=${DXB_SCREEN_ENV[CARD]:-}
  case $what in
    xvnc)
      printf '%s\n' "$DXB_XVNC" -displayfd 3 -rfbunixpath "$sock" -rfbunixmode 0660 -rfbport -1 -SecurityTypes None \
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
# _dxb_screen_ensure_dir: the screens directory as tmpfiles makes it, when it is missing.
_dxb_screen_ensure_dir() {
  local d; d=$(_dxb_screens_dir)
  [[ -d $d ]] && return 0
  mkdir -p "$d" && chmod 2770 "$d" && "$DXB_CHOWN" "root:$DXB_APPS_SCREEN_GROUP" "$d"
}

# _dxb_screen_put FILE CONTENT MODE OWNER: FILE in the screens directory, made new with mktemp and
# renamed over whatever was there (a symlink a group member planted is replaced, never written
# through). 0 ok, 1 failed.
_dxb_screen_put() {
  local f=$1 tmp
  tmp=$(mktemp "${f%/*}/.dxbtmp.XXXXXXXX") || return 1
  if printf '%s\n' "$2" > "$tmp" && chmod "$3" "$tmp" && "$DXB_CHOWN" "$4" "$tmp" && mv -fT "$tmp" "$f"; then return 0; fi
  rm -f "$tmp"
  return 1
}

# dxb_screen_mark_waiting S: S waits for its radio (radio.sh's apply starts it when the radio is
# back). Always 0: a marker that cannot be written is a warning.
dxb_screen_mark_waiting() {
  local f; f=$(_dxb_screens_dir)/$1.waiting
  { _dxb_screen_ensure_dir && _dxb_screen_put "$f" '' 600 root:root; } 2> /dev/null \
    || dxb_warn "could not write $f; start $1 by hand when its radio is back"
  return 0
}

# _dxb_screen_own APP RADIO: the program's settings to dxbapps (prepare's first fill and wiring
# write them as root): the directory of app_APP_settings_file RADIO and everything under it, and
# every directory between it and the program home on its own. chown never follows a symlink here.
_dxb_screen_own() {
  local f d p
  declare -F "app_$1_settings_file" > /dev/null || return 0
  f=$("app_$1_settings_file" "$2") || return 0
  d=${f%/*}
  [[ $d == "$DXB_APPS_HOME"/* && $d != *..* && -d $d ]] || return 0
  dxb_apps_own "$d" || return 1
  p=${d%/*}
  while [[ $p == "$DXB_APPS_HOME"/* ]]; do
    "$DXB_CHOWN" -h "$DXB_APPS_USER:$DXB_APPS_USER" "$p" || return 1
    p=${p%/*}
  done
}

# _dxb_screen_wait_rigctld RADIO WIRING: when the program talks to the radio through rigctld (it
# has CAT, or rigctld keys its PTT), wait up to DXB_RIGCTLD_WAIT_S for rigctld@RADIO; the
# program starts anyway after that (it shows its own rig error). Always 0.
_dxb_screen_wait_rigctld() {
  local u="rigctld@$1.service" i
  [[ $(jq -r --arg n "$1" '.radios[$n].cat != null' <<< "$DXB_RADIOS") == true || $(jq -r .ptt <<< "$2") == cat ]] || return 0
  for (( i = 0; ; i++ )); do
    systemctl is-active --quiet "$u" && return 0
    (( i >= DXB_RIGCTLD_WAIT_S )) && break
    command sleep 1
  done
  dxb_warn "$u is not active after ${DXB_RIGCTLD_WAIT_S}s; starting the program anyway"
  return 0
}

# dxb_screen_prepare S: the unit's ExecStartPre (root; DXB_RADIOS loaded, DXB_RADIO_SCAN fresh,
# DXB_CFG loaded for the first fill). RADIO must exist, be owned by APP, APP an installed session
# program, RADIO plugged in. Then: the first fill once per program per radio (app_APP_firstfill,
# then its mark); the wiring (app_APP_wire) whenever the radio's wiring hash differs from the one
# last wired for S (ruling 7; a radio set to "names" wiring is never wired); the settings handed to
# dxbapps; S.env written; a fresh S.d (client.conf: no sound server may spawn); a stale S.sock
# removed; rigctld waited for. 0 ok, 2 bad name, 3 not owned / not installed / unknown, 4 radio
# absent, 6 the session's files could not be written, 7 first fill or wiring failed.
dxb_screen_prepare() {
  local s=$1 ar a r w h hf env dir
  ar=$(dxb_screen_split "$s") || return 2
  a=${ar% *}; r=${ar#* }
  dxb_radio_get "$r" > /dev/null || return 3
  [[ $(jq -r --arg n "$r" '.radios[$n].owner' <<< "$DXB_RADIOS") == "$a" ]] || { dxb_error "radio $r is not $a's"; return 3; }
  dxb_app_load_owner "$a" || return 3
  dxb_app_is_session "$a" || { dxb_error "$a does not run in a screen session"; return 3; }
  dxb_radio_present "$r" || { dxb_error "radio $r is not plugged in"; return 4; }
  if ! dxb_apps_firstfill_done "$a" "$r"; then
    if declare -F "app_${a}_firstfill" > /dev/null; then "app_${a}_firstfill" "$r" || { dxb_error "$s: the first fill failed"; return 7; }; fi
    dxb_apps_firstfill_mark "$a" "$r" || dxb_warn "$s: could not mark the first fill done; it runs again next time"
  fi
  w=$(dxb_apps_wiring "$r") || return 3
  h=$(jq -r .hash <<< "$w"); hf=$DXB_APPS_STATE/wired/$s
  if [[ $(cat "$hf" 2> /dev/null) != "$h" && $(jq -r --arg n "$r" '.radios[$n].wiring' <<< "$DXB_RADIOS") != names ]]; then
    if declare -F "app_${a}_wire" > /dev/null; then "app_${a}_wire" "$r" || { dxb_error "$s: wiring $a to radio $r failed"; return 7; }; fi
    { mkdir -p "${hf%/*}" && printf '%s\n' "$h" > "$hf"; } 2> /dev/null \
      || dxb_warn "$s: could not record the wiring; it is wired again next time"
  fi
  _dxb_screen_own "$a" "$r" || dxb_warn "$s: could not hand $a's settings to $DXB_APPS_USER"
  env=$(dxb_screen_env "$s") || return 3
  dir=$(dxb_screen_dir "$s")
  _dxb_screen_ensure_dir || { dxb_error "could not create $(_dxb_screens_dir)"; return 6; }
  _dxb_screen_put "$(dxb_screen_envfile "$s")" "$env" 640 "root:$DXB_APPS_SCREEN_GROUP" \
    || { dxb_error "could not write $(dxb_screen_envfile "$s")"; return 6; }
  rm -f "$(dxb_screen_sock "$s")"
  rm -rf "$dir"
  # client.conf is written while S.d is still root's, then the file and the directory are given
  # to dxbapps (mkdir fails on anything a group member put back in the meantime)
  if ! { mkdir -m 700 "$dir" && printf 'autospawn = no\n' > "$dir/client.conf" \
      && "$DXB_CHOWN" -h "$DXB_APPS_USER:$DXB_APPS_USER" "$dir/client.conf" && "$DXB_CHOWN" -h "$DXB_APPS_USER:$DXB_APPS_USER" "$dir"; }; then
    dxb_error "could not prepare $dir"; return 6
  fi
  _dxb_screen_wait_rigctld "$r" "$w"
}

# dxb_screen_stopped S: after the session ended (its ExecStopPost, root). A radio that is gone
# right now leaves S waiting for it, so a program that ended because its card vanished comes back
# on replug; anything else (quit from its menu, Stop) leaves nothing. Always 0.
dxb_screen_stopped() {
  local ar r
  ar=$(dxb_screen_split "$1" 2> /dev/null) || return 0
  r=${ar#* }
  rm -rf "$(dxb_screen_dir "$1")"
  rm -f "$(dxb_screen_sock "$1")"
  dxb_radio_get "$r" > /dev/null 2>&1 || return 0
  dxb_radio_present "$r" || dxb_screen_mark_waiting "$1"
  return 0
}

# dxb_screen_unit_state UNIT: running | starting | stopped | failed.
dxb_screen_unit_state() {
  case $(systemctl is-active "$1" 2> /dev/null) in
    active|reloading) echo running ;;
    activating) echo starting ;;
    failed) echo failed ;;
    *) if systemctl is-failed --quiet "$1" 2> /dev/null; then echo failed; else echo stopped; fi ;;
  esac
}
# dxb_screen_state S: dxb_screen_unit_state of S's unit.
dxb_screen_state() { dxb_screen_unit_state "dxberry-screen@$1.service"; }

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

# dxb_screen_session_run S: the session itself. Reads S.env (never the radio record), starts Xvnc
# (SCREEN=yes: its display number comes back on fd 3) and openbox, the private PulseAudio
# (AUDIO=pulse), then the program (app_APP_command RADIO INDEX, one argument per line) with
# HOME, DISPLAY, XDG_RUNTIME_DIR=S.d and PULSE_SERVER / PULSE_CLIENTCONFIG set for every program
# (an ALSA one too: nothing may spawn a sound server). The program also sees APP, RADIO, INDEX,
# DEVICE, CARD, RIGCTLD and DXB_APPS_OPT. Ends with the program's own exit status; on TERM the
# program gets TERM, then KILL after DXB_SCREEN_STOP_WAIT_S (ruling 9), and the session ends 0 (a
# stop is not a failure). Xvnc and PulseAudio ending first end the program too (status 1); the
# helpers always end with the program.
dxb_screen_session_run() {
  local s=$1 envf dir fifo n='' rfd i rc=0 done_pid a r
  local xpid='' opid='' ppid='' prog='' why=''
  local -a argv=() xv=() ob=() pa=() live=()
  envf=$(dxb_screen_envfile "$s"); dir=$(dxb_screen_dir "$s")
  [[ -r $envf && -d $dir ]] || { echo "$envf or $dir is missing: the session's prepare step did not run" >&2; return 6; }
  dxb_screen_parse "$(< "$envf")"
  a=${DXB_SCREEN_ENV[APP]:-}; r=${DXB_SCREEN_ENV[RADIO]:-}
  [[ "$a-$r" == "$s" ]] || { echo "$envf is not $s's" >&2; return 6; }
  dxb_app_load "$a" || return 3
  declare -F "app_${a}_command" > /dev/null || { echo "$a has no command to run" >&2; return 6; }
  mapfile -t argv < <("app_${a}_command" "$r" "${DXB_SCREEN_ENV[INDEX]:-0}")
  [[ ${#argv[@]} -gt 0 && -n ${argv[0]} ]] || { echo "$a gave no command to run" >&2; return 6; }
  export HOME=$DXB_APPS_HOME XDG_RUNTIME_DIR=$dir PULSE_SERVER="unix:$dir/pulse.sock" PULSE_CLIENTCONFIG="$dir/client.conf" \
    APP=$a RADIO=$r INDEX=${DXB_SCREEN_ENV[INDEX]:-} DEVICE=${DXB_SCREEN_ENV[DEVICE]:-} CARD=${DXB_SCREEN_ENV[CARD]:-} \
    RIGCTLD=${DXB_SCREEN_ENV[RIGCTLD]:-} DXB_APPS_OPT
  unset DISPLAY
  _DXB_SCREEN_TERM=0
  trap '_DXB_SCREEN_TERM=1' TERM INT HUP
  if [[ ${DXB_SCREEN_ENV[SCREEN]:-yes} == yes ]]; then
    mapfile -t xv < <(dxb_screen_session_argv "$s" xvnc "$envf")
    mapfile -t ob < <(dxb_screen_session_argv "$s" openbox "$envf")
    fifo=$dir/display
    rm -f "$fifo"
    mkfifo -m 600 "$fifo" || { echo "could not create $fifo" >&2; return 6; }
    # read-write, so this open never blocks; the display number arrives once Xvnc listens
    exec {rfd}<> "$fifo"
    "${xv[@]}" 3> "$fifo" {rfd}<&- &
    xpid=$!
    for (( i = 0; i < 30; i++ )); do
      read -r -t 1 -u "$rfd" n && break
      kill -0 "$xpid" 2> /dev/null || break
      (( _DXB_SCREEN_TERM )) && break
    done
    exec {rfd}<&-
    rm -f "$fifo"
    if [[ ! $n =~ ^[0-9]+$ ]]; then
      (( _DXB_SCREEN_TERM )) || echo "Xvnc did not start for $s" >&2
      _dxb_screen_end "$xpid"
      (( _DXB_SCREEN_TERM )) && return 0
      return 6
    fi
    export DISPLAY=":$n"
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
  trap - TERM INT HUP
  case $why in
    term) return 0 ;;
    helper) return 1 ;;
  esac
  return $rc
}
