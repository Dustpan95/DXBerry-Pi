#!/bin/bash
# shellcheck shell=bash
# dxberry.txt: parsing, validation, defaults and derived values.
#
#   dxb_config_load FILE       fills DXB_CFG / DXB_CFG_LINES; unknown keys -> DXB_CFG_WARNINGS
#   dxb_config_validate        applies defaults, checks every rule, fills DXB_CFG_ERRORS; 0 = valid
#   dxb_config_get KEY         prints a value ('' if unset)
#   dxb_config_print_masked    prints the effective config with secrets masked
#
# Derived keys set by dxb_config_validate (leading underscore, never read from the file):
#   _MODE static|dhcp  _IP  _PREFIX  _WIFI 0|1  _BEACON 0|1  _SEND_PATH is_only|rf|both
#   _SYMBOL_TABLE  _SYMBOL  _INTERVAL_S

declare -gA DXB_CFG=() DXB_CFG_LINES=()
declare -ga DXB_CFG_ERRORS=() DXB_CFG_WARNINGS=()
# shellcheck disable=SC2034
DXB_APPLIED='<applied>'

DXB_KNOWN_KEYS='HOSTNAME PASSWORD TIMEZONE STATIC_IP GATEWAY DNS WIFI_SSID WIFI_PASSWORD WIFI_COUNTRY CALLSIGN LATITUDE LONGITUDE BEACON_COMMENT BEACON_INTERVAL_MIN IGATE_SERVER WEBUI_USER WEBUI_PASSWORD SSH_PUBKEY BEACON_SEND BEACON_PATH BEACON_SYMBOL DIGIPEATER IGATE_RF_TO_IS IGATE_IS_TO_RF GRAYWOLF_VERSION SERIAL_CONSOLE'
DXB_SECRET_KEYS='PASSWORD WIFI_PASSWORD WEBUI_PASSWORD'
DXB_RESERVED_PREFIXES='PAT_ WSJTX_ JS8CALL_ FLDIGI_ RIG_ GPS_ CONSOLE_'

_dxb_key_known() { local k; for k in $DXB_KNOWN_KEYS; do [[ $k == "$1" ]] && return 0; done; return 1; }
_dxb_key_reserved() { local p; for p in $DXB_RESERVED_PREFIXES; do [[ $1 == "$p"* ]] && return 0; done; return 1; }

dxb_config_load() {
  local file=$1 line key val n=0
  DXB_CFG=(); DXB_CFG_LINES=(); DXB_CFG_ERRORS=(); DXB_CFG_WARNINGS=()
  [[ -r $file ]] || { DXB_CFG_ERRORS+=("cannot read $file"); return 1; }
  while IFS= read -r line || [[ -n $line ]]; do
    n=$((n + 1))
    line=${line%$'\r'}
    line=${line#"${line%%[![:space:]]*}"}
    [[ -z $line || $line == \#* ]] && continue
    if [[ $line != *=* ]]; then
      DXB_CFG_ERRORS+=("line $n: expected KEY=value, got: $line")
      continue
    fi
    key=${line%%=*}
    val=${line#*=}
    key=${key%"${key##*[![:space:]]}"}
    val=${val#"${val%%[![:space:]]*}"}
    val=${val%"${val##*[![:space:]]}"}
    if [[ ${#val} -ge 2 && $val == \"*\" ]]; then val=${val:1:${#val}-2}; fi
    if [[ ! $key =~ ^[A-Z][A-Z0-9_]*$ ]]; then
      DXB_CFG_ERRORS+=("line $n: invalid key '$key'")
      continue
    fi
    _dxb_key_reserved "$key" && continue
    if ! _dxb_key_known "$key"; then
      DXB_CFG_WARNINGS+=("line $n: unknown key '$key' ignored")
      continue
    fi
    DXB_CFG[$key]=$val
    # shellcheck disable=SC2034
    DXB_CFG_LINES[$key]=$n
  done < "$file"
  return 0
}

dxb_config_get() { printf '%s' "${DXB_CFG[$1]:-}"; }

dxb_config_print_masked() {
  local k v
  for k in $DXB_KNOWN_KEYS; do
    v=${DXB_CFG[$k]:-}
    case " $DXB_SECRET_KEYS " in
      *" $k "*) [[ -z $v || $v == "$DXB_APPLIED" ]] || v='********' ;;
    esac
    printf '%s=%s\n' "$k" "$v"
  done
}
