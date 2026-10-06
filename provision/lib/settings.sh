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

_dxb_settings_in() { [[ " $1 " == *" $2 "* ]]; }
dxb_settings_is_console_key() { _dxb_settings_in "$DXB_CONSOLE_KEYS" "$1"; }
dxb_settings_is_network_key() { _dxb_settings_in "$DXB_NETWORK_KEYS" "$1"; }
dxb_settings_is_secret() { _dxb_settings_in "$DXB_SECRET_KEYS" "$1"; }
# dxb_settings_value_ok VALUE: a dxberry.txt value is one line of printable text.
dxb_settings_value_ok() { [[ $1 != *[[:cntrl:]]* ]]; }

# dxb_settings_get_json FILE: the console keys of FILE (spec 10.2 "get"): each key's value as
# written and the value in effect after the validator's defaults; secrets only as set / not set,
# never their value. Secrets never reach jq's arguments: only "true"/"false" does.
dxb_settings_get_json() {
  local file=$1 k valid=true out='{}' errs
  local -A raw=()
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
  jq -cn --arg f "$file" --argjson v "$valid" --argjson e "$errs" --argjson k "$out" '{file: $f, valid: $v, errors: $e, keys: $k}'
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
