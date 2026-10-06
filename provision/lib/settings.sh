#!/bin/bash
# shellcheck shell=bash
# The console's settings (console spec section 10): read and change the Pi-level keys of
# dxberry.txt, and the network safety net that undoes a network change nobody kept.

: "${DXB_RUN_DIR:=/run/dxberry}"
: "${DXB_CONFIG_PREV:=$DXB_STATE_DIR/dxberry.txt.prev}"

# The keys the console changes (spec 10.1). Station, beacon, iGate and digipeater keys are changed
# in Graywolf's own page; dxberry.txt seeds them only at first boot and on --reseed.
DXB_CONSOLE_KEYS='STATIC_IP GATEWAY DNS WIFI_SSID WIFI_PASSWORD WIFI_COUNTRY HOSTNAME TIMEZONE PASSWORD SSH_PUBKEY GPS_DEVICE GPS_BAUD GPS_PPS POSITION_LOG CONSOLE'
# Changing one of these arms the network safety net (spec 10.3); they are saved on their own.
DXB_NETWORK_KEYS='STATIC_IP GATEWAY DNS WIFI_SSID WIFI_PASSWORD WIFI_COUNTRY'
# Network settings from the console are off (0) until a live re-apply is built and tested: a running
# interface keeps its settings until it is cycled or the Pi restarts (restarting dxberry-netwatch
# adopts it as it is), so the safety net below could not try a change before Keep commits it.
# 1 turns them on; the tests do, so that code stays tested and ready (spec 10.3).
: "${DXB_CONFIG_NETWORK:=0}"

_dxb_settings_in() { [[ " $1 " == *" $2 "* ]]; }
dxb_settings_is_console_key() { _dxb_settings_in "$DXB_CONSOLE_KEYS" "$1"; }
dxb_settings_is_network_key() { _dxb_settings_in "$DXB_NETWORK_KEYS" "$1"; }
dxb_settings_is_secret() { _dxb_settings_in "$DXB_SECRET_KEYS" "$1"; }
# dxb_settings_value_ok VALUE: a dxberry.txt value is one line of printable text.
dxb_settings_value_ok() { [[ $1 != *[[:cntrl:]]* ]]; }

# dxb_settings_get_json FILE: the console keys of FILE (spec 10.2 "get"): each key's value as
# written and the value in effect after the validator's defaults; secrets only as set / not set,
# never their value. Secrets never reach jq's arguments: only "true"/"false" does.
# network_editable: whether set takes network keys (DXB_CONFIG_NETWORK).
dxb_settings_get_json() {
  local file=$1 k valid=true out='{}' errs ne=false
  local -A raw=()
  [[ $DXB_CONFIG_NETWORK == 1 ]] && ne=true
  dxb_config_load "$file" || valid=false
  for k in $DXB_CONSOLE_KEYS; do raw[$k]=${DXB_CFG[$k]:-}; done
  dxb_config_validate || valid=false
  for k in $DXB_CONSOLE_KEYS; do
    if dxb_settings_is_secret "$k"; then
      out=$(jq -c --arg k "$k" --argjson s "$([[ -n ${raw[$k]} ]] && echo true || echo false)" '.[$k] = {secret: true, set: $s}' <<< "$out")
    else
      out=$(jq -c --arg k "$k" --arg v "${raw[$k]}" --arg e "${DXB_CFG[$k]:-}" '.[$k] = {value: $v, effective: $e}' <<< "$out")
    fi
  done
  errs=$(printf '%s\n' "${DXB_CFG_ERRORS[@]}" | jq -Rsc 'split("\n") | map(select(length > 0))')
  jq -cn --arg f "$file" --argjson v "$valid" --argjson e "$errs" --argjson ne "$ne" --argjson k "$out" \
    '{file: $f, valid: $v, errors: $e, network_editable: $ne, keys: $k}'
}

# dxb_settings_edit KEY VALUE < CONTENT: CONTENT with KEY set to VALUE. The first active KEY line
# (any spacing around "=") is rewritten, later active ones are dropped, comments and every other
# line stay as they are; with no such line, one is appended. An empty VALUE leaves "KEY=" (unset:
# the default applies). KEY and VALUE reach awk through its environment, never its arguments.
dxb_settings_edit() {
  k=$1 v=$2 awk '
    $0 ~ ("^[[:blank:]]*" ENVIRON["k"] "[[:blank:]]*=") { if (!done) { print ENVIRON["k"] "=" ENVIRON["v"]; done = 1 }; next }
    { print }
    END { if (!done) print ENVIRON["k"] "=" ENVIRON["v"] }'
}

# _dxb_settings_tmp CONTENT: a private temp file holding CONTENT (it may hold a secret) under
# DXB_STATE_DIR (0700, like every other file that holds one); prints its path. 6 when it cannot be written.
_dxb_settings_tmp() {
  local t
  mkdir -p "$DXB_STATE_DIR" 2> /dev/null
  t=$(umask 077; mktemp -p "$DXB_STATE_DIR" .settings.XXXXXX) || return 6
  printf '%s\n' "$1" > "$t" || { rm -f "$t"; return 6; }
  printf '%s\n' "$t"
}

# dxb_settings_validate CONTENT: the base validator over CONTENT, in a subshell so the caller's
# DXB_CFG is untouched. Prints its messages one per line. 0 valid, 4 not, 6 no temp file.
dxb_settings_validate() {
  local t rc
  t=$(_dxb_settings_tmp "$1") || return 6
  ( dxb_config_load "$t" > /dev/null 2>&1; dxb_config_validate > /dev/null 2>&1; rc=$?
    (( ${#DXB_CFG_ERRORS[@]} )) && printf '%s\n' "${DXB_CFG_ERRORS[@]}"
    exit $rc )
  rc=$?
  rm -f "$t"
  (( rc == 0 )) && return 0
  return 4
}

# dxb_settings_changed OLD NEW: the console keys whose value as written differs, one per line,
# sorted. Values never leave this function (secrets are compared, not printed).
dxb_settings_changed() {
  local a b
  a=$(_dxb_settings_tmp "$1") || return 6
  b=$(_dxb_settings_tmp "$2") || { rm -f "$a"; return 6; }
  (
    local k
    local -A old=()
    dxb_config_load "$a" > /dev/null 2>&1
    for k in $DXB_CONSOLE_KEYS; do old[$k]=${DXB_CFG[$k]:-}; done
    dxb_config_load "$b" > /dev/null 2>&1
    for k in $DXB_CONSOLE_KEYS; do [[ ${old[$k]} == "${DXB_CFG[$k]:-}" ]] || echo "$k"; done
  ) | sort
  rm -f "$a" "$b"
}

# dxb_settings_write FILE CONTENT: keep FILE's current content as DXB_CONFIG_PREV (0600), then
# write CONTENT with a temp file and a rename in FILE's directory (atomic, and it works on the FAT
# boot partition) and read it back. 0 written, 6 failed.
dxb_settings_write() {
  local file=$1 content=$2
  mkdir -p "$(dirname "$DXB_CONFIG_PREV")" 2> /dev/null
  if ! ( umask 077; cp "$file" "$DXB_CONFIG_PREV" ) 2> /dev/null; then
    dxb_error "could not keep the previous dxberry.txt as $DXB_CONFIG_PREV"
    return 6
  fi
  chmod 600 "$DXB_CONFIG_PREV" 2> /dev/null
  dxb_write_if_changed "$file" "$content" > /dev/null 2>&1
  [[ -f $file && $(< "$file") == "$content" ]] || { dxb_error "could not write $file"; return 6; }
}

# ---- the network safety net (spec 10.3) ----------------------------------------------------
: "${DXB_NETSAFE_DIR:=$DXB_STATE_DIR/network-snapshot}"
: "${DXB_NETSAFE_AT:=$DXB_RUN_DIR/config-revert-at}"
: "${DXB_NETSAFE_FILES:=/etc/network/interfaces /etc/network/interfaces.d/eth0.conf /etc/network/interfaces.d/wlan0.conf /etc/resolv.conf /var/lib/dietpi/dietpi-wifi.db /etc/wpa_supplicant/wpa_supplicant.conf}"
: "${DXB_SYSTEMD_RUN:=systemd-run}"
: "${DXB_CONFIG_CMD:=/opt/dxberry/bin/dxberry-config}"
: "${DXB_NETSAFE_BACKSTOP_S:=600}"
: "${DXB_NETSAFE_WINDOW_S:=120}"
: "${DXB_TEMPLATES:=/opt/dxberry/templates}"

# dxb_netsafe_files: every file a network change can touch, one per line - DietPi's and ifupdown's
# network files, the WiFi credentials, and dxberry.txt itself (a replaced WiFi password is scrubbed
# from dxberry.txt and lives on only in this snapshot).
dxb_netsafe_files() {
  local boot f
  boot=$(dxb_boot_dir)
  for f in $DXB_NETSAFE_FILES "$boot/dxberry.txt" "$boot/dietpi-wifi.txt"; do printf '%s\n' "$f"; done
}

# dxb_netsafe_snapshot: copy every network file that exists into DXB_NETSAFE_DIR (0700) with a
# manifest of which existed; a file the change creates is removed on restore. The copies hold the
# WiFi key, so nothing here prints them. 0 ok, 6 failed (no snapshot left behind).
dxb_netsafe_snapshot() {
  local f i=0
  rm -rf "$DXB_NETSAFE_DIR"
  ( umask 077; mkdir -p "$DXB_NETSAFE_DIR" ) || { dxb_error "could not create $DXB_NETSAFE_DIR"; return 6; }
  chmod 700 "$DXB_NETSAFE_DIR"
  while IFS= read -r f; do
    i=$(( i + 1 ))
    if [[ -f $f ]]; then
      if ! cp -p "$f" "$DXB_NETSAFE_DIR/$i"; then
        dxb_error "could not snapshot $f"; rm -rf "$DXB_NETSAFE_DIR"; return 6
      fi
      printf '%s\t%s\n' "$i" "$f" >> "$DXB_NETSAFE_DIR/manifest.new"
    else
      printf '%s\t%s\n' - "$f" >> "$DXB_NETSAFE_DIR/manifest.new"
    fi
  done < <(dxb_netsafe_files)
  # the manifest appears last: its presence is what "a change is waiting" means
  mv -f "$DXB_NETSAFE_DIR/manifest.new" "$DXB_NETSAFE_DIR/manifest"
}

dxb_netsafe_pending() { [[ -f $DXB_NETSAFE_DIR/manifest ]]; }

# _dxb_netsafe_put SRC DEST: DEST becomes a copy of SRC through a temp file and a rename (so a
# crash never leaves a half-written dxberry.txt), with SRC's mode where the filesystem has modes.
_dxb_netsafe_put() {
  mkdir -p "$(dirname "$2")" 2> /dev/null
  cp "$1" "$2.dxbtmp.$$" || return 1
  chmod --reference="$1" "$2.dxbtmp.$$" 2> /dev/null || true
  mv -f "$2.dxbtmp.$$" "$2"
}

# dxb_netsafe_restore: put the snapshot back - files that existed are restored, files that did not
# are removed - then drop the snapshot. 0 ok, 6 when a file could not be restored (the snapshot is
# kept so it can be retried).
dxb_netsafe_restore() {
  local n f rc=0
  dxb_netsafe_pending || { dxb_error "there is no network change to undo"; return 6; }
  while IFS=$'\t' read -r n f; do
    [[ -n $f ]] || continue
    if [[ $n == - ]]; then
      rm -f "$f" || { dxb_error "could not remove $f"; rc=6; }
    else
      _dxb_netsafe_put "$DXB_NETSAFE_DIR/$n" "$f" || { dxb_error "could not restore $f"; rc=6; }
    fi
  done < "$DXB_NETSAFE_DIR/manifest"
  (( rc == 0 )) && rm -rf "$DXB_NETSAFE_DIR"
  return $rc
}

_dxb_netsafe_unit() { [[ -f $DXB_NETSAFE_AT ]] && awk '{ print $1; exit }' "$DXB_NETSAFE_AT"; }
dxb_netsafe_revert_at() { [[ -f $DXB_NETSAFE_AT ]] && awk '{ print $2; exit }' "$DXB_NETSAFE_AT"; return 0; }

# dxb_netsafe_arm SECONDS: arm a timer that runs "dxberry-config revert" SECONDS from now, then stop
# the one armed before, so there is never a moment without one, and record the new unit and its
# deadline for the page's countdown. Each arm has its own unit name: a transient timer cannot be
# armed twice under one name. 0 ok, 6 failed (the earlier timer stays armed).
dxb_netsafe_arm() {
  local unit old
  unit="dxberry-config-revert-$(date +%s%N)"
  "$DXB_SYSTEMD_RUN" --quiet --collect --on-active="$1" --unit="$unit" "$DXB_CONFIG_CMD" revert > /dev/null 2>&1 \
    || { dxb_error "could not arm the network undo timer"; return 6; }
  old=$(_dxb_netsafe_unit)
  [[ -n $old ]] && systemctl stop "$old.timer" > /dev/null 2>&1
  mkdir -p "$DXB_RUN_DIR" 2> /dev/null
  printf '%s %s\n' "$unit" "$(( $(date +%s) + $1 ))" > "$DXB_NETSAFE_AT"
}

dxb_netsafe_disarm() {
  local u
  u=$(_dxb_netsafe_unit)
  [[ -n $u ]] && systemctl stop "$u.timer" > /dev/null 2>&1
  rm -f "$DXB_NETSAFE_AT"
  return 0
}

# dxb_netsafe_install_unit: the boot unit that undoes a change still waiting at boot (a transient
# timer does not survive a restart). 0 installed or unchanged, 6 failed. The unit's path is worked
# out here, not when this file is sourced, so a caller that sets DXB_SYSTEMD_DIR later still wins.
dxb_netsafe_install_unit() {
  local content f=${DXB_NETSAFE_UNIT_FILE:-${DXB_SYSTEMD_DIR:-/etc/systemd/system}/dxberry-config-boot.service}
  content=$(< "$DXB_TEMPLATES/dxberry-config-boot.service") || { dxb_error "dxberry-config-boot.service template missing"; return 6; }
  mkdir -p "$(dirname "$f")" 2> /dev/null
  if dxb_write_if_changed "$f" "$content"; then systemctl daemon-reload > /dev/null 2>&1; fi
  [[ -f $f && $(< "$f") == "$content" ]] || { dxb_error "could not write $f"; return 6; }
  systemctl enable dxberry-config-boot.service > /dev/null 2>&1 || { dxb_error "could not enable dxberry-config-boot.service"; return 6; }
}

# ---- the settings job ----------------------------------------------------------------------
: "${DXB_CONFIG_JOB_FILE:=$DXB_RUN_DIR/config-job.json}"
: "${DXB_CONFIG_RESULT:=$DXB_RUN_DIR/config-result.json}"
: "${DXB_CONFIG_PENDING:=$DXB_RUN_DIR/config-pending}"
: "${DXB_CONFIG_REVERTED:=$DXB_STATE_DIR/config-reverted}"

dxb_settings_job_unit() { [[ -f $DXB_CONFIG_JOB_FILE ]] && jq -r '.unit // empty' "$DXB_CONFIG_JOB_FILE" 2> /dev/null; return 0; }
dxb_settings_job_running() { local u; u=$(dxb_settings_job_unit); [[ -n $u ]] && systemctl is-active --quiet "$u"; }

# dxb_settings_push_graywolf: Graywolf keeps its own copy of POSITION_LOG and the GPS source, and a
# plain provisioner run does not reseed it, so the new values go through their one seed function
# each (spec 10.2) - never a whole --reseed. 0 ok, 1 failed (the seed functions record why).
dxb_settings_push_graywolf() {
  local rc=0
  # shellcheck disable=SC2015  # intentional: either failure takes the same "not updated" error path
  dxb_config_load "$(dxb_boot_dir)/dxberry.txt" && dxb_config_validate \
    || { dxb_error "dxberry.txt does not validate; Graywolf's position log and GPS source not updated"; return 1; }
  dxb_gw_wait_ready || { dxb_error "Graywolf's API is not answering; its position log and GPS source not updated"; return 1; }
  rm -f "$DXB_GW_COOKIES"; ( umask 077; : > "$DXB_GW_COOKIES" )
  dxb_gw_login_any || { rm -f "$DXB_GW_COOKIES"; return 1; }
  dxb_gw_seed_position_log || rc=1
  dxb_gw_seed_gps || rc=1
  dxb_gw_api POST /auth/logout > /dev/null 2>&1 || true
  rm -f "$DXB_GW_COOKIES"
  return $rc
}
