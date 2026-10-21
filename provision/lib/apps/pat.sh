#!/bin/bash
# shellcheck shell=bash
# Pat (Winlink) with the ARDOP modem as a program on a radio (applications spec; ruling 5: single
# copy, one web interface and one ARDOP port). Pat's web page listens on 127.0.0.1:8180 and
# reaches the browser only through the nginx proxy on 8081, behind the console's login
# (webproxy.sh). The first fill and the wiring run as dxbapps (dxberry-app settings): nothing here
# changes ownership, writes as root or writes under DXB_APPS_STATE.
#
# Settings file ~/.config/pat/config.json. Key names, from Pat v1.0.0 (la5nta/pat, cfg/config.go):
#   mycall (line 48), secure_login_password (53), locator (62), http_addr (78),
#   connect_aliases (89), hamlib_rigs (97), ardop (103);
#   HamlibConfig (cfg/config.go:194-): network (194), address (200), VFO;
#   ArdopConfig (cfg/config.go:208-): addr (208), rig (219), ptt_ctrl (222), plus arq_bandwidth,
#   connect_requests, beacon_interval and cwid_enabled.
# Pat's loader (app/config.go ReadConfig) unmarshals the file into a ZERO Config, not into Pat's
# defaults, so a file holding only DXBerry's keys would lose every default. So when the file is
# missing, Pat itself writes its full default (`pat --config FILE mps list` creates it, mode 0600,
# then exits non-zero with "Missing mycall"; that exit is expected and ignored) and DXBerry's keys
# are set into that with jq. Only the keys below change; every other key stays the operator's:
#   first fill: mycall, locator (when known), http_addr
#   wiring:     ardop.addr, ardop.rig (RADIO, only with CAT PTT, else removed), ardop.ptt_ctrl,
#               hamlib_rigs.RADIO.address / .network (the rigctld of the radio; the VFO stays)
# Expects common.sh, inifile.sh, apps.sh, webproxy.sh and radio.sh to be sourced.

: "${DXB_PAT_CMD:=pat}"

app_pat_kind() { echo radio; }
app_pat_session() { echo yes; }
app_pat_label() { echo "Pat (Winlink)"; }
app_pat_multi() { echo no; }
app_pat_audio() { echo alsa; }
app_pat_screen() { echo no; }
app_pat_needs_service_restart() { echo no; }
app_pat_installed() { dxb_apps_installed pat; }
app_pat_version() { dxb_apps_version pat; }
app_pat_unit() { dxb_app_session_unit pat "$1"; }
# nothing to undo: the settings file stays for the next time the radio is claimed
app_pat_unwire() { return 0; }
# the path does not depend on the radio (single copy)
app_pat_settings_file() { printf '%s/.config/pat/config.json\n' "$DXB_APPS_HOME"; }
# the page opens Pat's proxy in a browser tab
app_pat_open() { printf '%s\n' '{"kind":"url","port":8081,"path":"/"}'; }

# _dxb_pat_edit ARGS... FILTER: the settings file passed through a jq filter and written back
# atomically (a 0600 temp file in the same directory, then mv). A missing file is made first by
# Pat itself; if it still is not there the filter starts from {} (one warning). A file jq cannot
# read is left as it is. 0 written, 6 failed.
_dxb_pat_edit() {
  local f tmp filter=${*: -1}
  local -a args=("${@:1:$#-1}")
  f=$(app_pat_settings_file)
  mkdir -p "${f%/*}" || return 6
  if [[ ! -e $f ]]; then
    "$DXB_PAT_CMD" --config "$f" mps list > /dev/null 2>&1 || true
    [[ -s $f ]] || { echo "pat: Pat did not write its defaults; starting $f from an empty file" >&2; rm -f "$f"; }
  fi
  tmp=$(umask 077; mktemp "$f.XXXXXX") || { echo "pat: could not write in ${f%/*}" >&2; return 6; }
  if [[ -e $f ]]; then
    jq "${args[@]}" "$filter" "$f" > "$tmp"
  else
    jq -n "${args[@]}" "$filter" > "$tmp"
  fi
  if [[ ! -s $tmp ]] || ! mv -f "$tmp" "$f"; then
    rm -f "$tmp"; echo "pat: could not update $f" >&2; return 6
  fi
}

# app_pat_firstfill RADIO: call sign, grid (when known) and the web address (loopback only).
# shellcheck disable=SC2016  # the jq filter is not shell text
app_pat_firstfill() {
  local st call grid
  st=$(dxb_apps_station) || return 6
  call=$(jq -r .call <<< "$st"); grid=$(jq -r .grid <<< "$st")
  _dxb_pat_edit --arg c "$call" --arg g "$grid" '
    (if $c != "" then .mycall = $c else . end)
    | (if $g != "" then .locator = $g else . end)
    | .http_addr = "127.0.0.1:8180"'
}

# app_pat_wire RADIO: ARDOP's address, its rig control and PTT, and RADIO's rigctld (these keys only).
# shellcheck disable=SC2016  # the jq filter is not shell text
app_pat_wire() {
  local w cat=false
  w=$(dxb_apps_wiring "$1") || return 6
  [[ $(jq -r .ptt <<< "$w") != cat ]] || cat=true
  _dxb_pat_edit --argjson cat "$cat" --arg addr "$(jq -r .rigctld <<< "$w")" '
    .ardop.addr = "localhost:8515"
    | (if $cat then .ardop.rig = "RADIO" | .ardop.ptt_ctrl = true else del(.ardop.rig) | .ardop.ptt_ctrl = false end)
    | .hamlib_rigs.RADIO = ((.hamlib_rigs.RADIO // {}) + {address: $addr, network: "tcp"})'
}

# app_pat_command RADIO INDEX: the command line, one argument per line (runs as dxbapps). Two
# processes: ARDOP in the background, Pat beside it; a fixed script, DEVICE and DXB_APPS_OPT come
# from the session's environment. ARDOP ends with Pat, and with SIGTERM from systemctl stop.
# shellcheck disable=SC2016  # the session script is not expanded here
app_pat_command() {
  # one line: the session runner reads one argument per line
  printf '%s\n' bash -c 'logdir=$HOME/.local/share/ardop; mkdir -p "$logdir" || exit 1; trap '\''kill $(jobs -p) 2> /dev/null; wait; exit 143'\'' TERM INT HUP; "$DXB_APPS_OPT/ardop/ardopcf" 8515 "$DEVICE" "$DEVICE" --logdir "$logdir" & pat http & patpid=$!; wait "$patpid"; rc=$?; kill $(jobs -p) 2> /dev/null; wait; exit "$rc"'
}

# ARDOP (ardopcf, the newest release for ARM) and the proxy; the .deb itself is Pat.
app_pat_install_extra() {
  dxb_apps_fetch_install ardop 'github:pflarue/ardop:bin=^ardopcf_arm_Linux_64$' ardopcf || return 6
  dxb_webproxy_install || return 6
  dxb_webproxy_site pat || return 6
}

app_pat_remove_extra() {
  rm -rf "${DXB_APPS_OPT:?}/ardop"
  dxb_webproxy_site_remove pat
}
