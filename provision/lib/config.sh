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
    # Windows Notepad writes UTF-8 with a BOM; without this every first line - usually a comment -
    # would be rejected as malformed and take the whole file down with it.
    if (( n == 1 )); then line=${line#$'\xef\xbb\xbf'}; fi
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

_dxb_err() { local key=$1; shift; local n=${DXB_CFG_LINES[$key]:-}; DXB_CFG_ERRORS+=("${n:+line $n: }$key $*"); }
_dxb_default() { [[ -n ${DXB_CFG[$1]:-} ]] || DXB_CFG[$1]=$2; }
_dxb_len_between() { local n=${#1}; (( n >= $2 && n <= $3 )); }
_dxb_is_onoff() { [[ $1 == on || $1 == off ]]; }
_dxb_is_ipv4() {
  [[ $1 =~ ^([0-9]{1,3})\.([0-9]{1,3})\.([0-9]{1,3})\.([0-9]{1,3})$ ]] || return 1
  local o
  for o in "${BASH_REMATCH[@]:1}"; do (( 10#$o <= 255 )) || return 1; done
}
_dxb_ip2int() { local IFS=. a b c d; read -r a b c d <<< "$1"; echo $(( (10#$a << 24) | (10#$b << 16) | (10#$c << 8) | 10#$d )); }
_dxb_same_subnet() {
  local a b mask
  a=$(_dxb_ip2int "$1"); b=$(_dxb_ip2int "$2")
  mask=$(( (0xFFFFFFFF << (32 - $3)) & 0xFFFFFFFF ))
  (( (a & mask) == (b & mask) ))
}
_dxb_in_range() {
  [[ $1 =~ ^-?[0-9]+(\.[0-9]+)?$ ]] || return 1
  awk -v v="$1" -v lo="$2" -v hi="$3" 'BEGIN { exit !(v + 0 >= lo + 0 && v + 0 <= hi + 0) }'
}

dxb_config_validate() {
  local v ip='' prefix='' lat lon zi
  _dxb_default HOSTNAME dxberry-pi
  _dxb_default TIMEZONE UTC
  _dxb_default BEACON_COMMENT 'DXBerry-Pi iGate'
  _dxb_default BEACON_INTERVAL_MIN 30
  _dxb_default IGATE_SERVER rotate.aprs2.net
  _dxb_default WEBUI_USER admin
  _dxb_default BEACON_SEND is
  _dxb_default BEACON_PATH 'WIDE1-1,WIDE2-1'
  _dxb_default BEACON_SYMBOL 'R&'
  _dxb_default DIGIPEATER off
  _dxb_default IGATE_RF_TO_IS on
  _dxb_default IGATE_IS_TO_RF off
  _dxb_default SERIAL_CONSOLE off

  v=${DXB_CFG[PASSWORD]:-}
  if [[ -z $v ]]; then _dxb_err PASSWORD 'is required'
  elif [[ $v != "$DXB_APPLIED" ]] && ! _dxb_len_between "$v" 8 100; then _dxb_err PASSWORD 'must be 8-100 characters'; fi

  [[ ${DXB_CFG[HOSTNAME]} =~ ^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$ ]] || _dxb_err HOSTNAME 'must be lowercase letters, digits and hyphens, 1-63 characters'

  v=${DXB_CFG[TIMEZONE]}; zi=${DXB_ZONEINFO_DIR:-/usr/share/zoneinfo}
  if [[ ! $v =~ ^[A-Za-z0-9_+/-]+$ ]]; then _dxb_err TIMEZONE 'is not a valid time zone name'
  elif [[ -d $zi && ! -f $zi/$v ]]; then _dxb_err TIMEZONE "'$v' is not a known time zone (e.g. America/Chicago)"; fi

  if [[ -n ${DXB_CFG[STATIC_IP]:-} ]]; then
    DXB_CFG[_MODE]=static
    if [[ ${DXB_CFG[STATIC_IP]} =~ ^([0-9.]+)/([0-9]{1,2})$ ]]; then ip=${BASH_REMATCH[1]}; prefix=$(( 10#${BASH_REMATCH[2]} )); fi
    if [[ -n $ip ]] && _dxb_is_ipv4 "$ip" && (( prefix >= 8 && prefix <= 30 )); then
      DXB_CFG[_IP]=$ip
      DXB_CFG[_PREFIX]=$prefix
      v=${DXB_CFG[GATEWAY]:-}
      if [[ -z $v ]]; then _dxb_err GATEWAY 'is required when STATIC_IP is set'
      elif ! _dxb_is_ipv4 "$v"; then _dxb_err GATEWAY 'is not a valid IPv4 address'
      elif ! _dxb_same_subnet "$ip" "$v" "$prefix"; then _dxb_err GATEWAY "is not inside ${DXB_CFG[STATIC_IP]}"; fi
      [[ -n ${DXB_CFG[DNS]:-} ]] || DXB_CFG[DNS]=${DXB_CFG[GATEWAY]:-}
      for v in ${DXB_CFG[DNS]}; do _dxb_is_ipv4 "$v" || _dxb_err DNS "'$v' is not a valid IPv4 address"; done
    else
      _dxb_err STATIC_IP 'must be an IPv4 address with prefix length, e.g. 192.168.1.90/24'
    fi
  else
    DXB_CFG[_MODE]=dhcp
    [[ -z ${DXB_CFG[GATEWAY]:-} ]] || DXB_CFG_WARNINGS+=('GATEWAY is ignored because STATIC_IP is not set (DHCP)')
  fi

  if [[ -n ${DXB_CFG[WIFI_SSID]:-} ]]; then
    DXB_CFG[_WIFI]=1
    _dxb_len_between "${DXB_CFG[WIFI_SSID]}" 1 32 || _dxb_err WIFI_SSID 'must be 1-32 characters'
    v=${DXB_CFG[WIFI_PASSWORD]:-}
    if [[ -z $v ]]; then _dxb_err WIFI_PASSWORD 'is required when WIFI_SSID is set'
    elif [[ $v != "$DXB_APPLIED" ]] && ! _dxb_len_between "$v" 8 63; then _dxb_err WIFI_PASSWORD 'must be 8-63 characters'; fi
    [[ ${DXB_CFG[WIFI_COUNTRY]:-} =~ ^[A-Z]{2}$ ]] || _dxb_err WIFI_COUNTRY 'must be a two-letter uppercase country code (e.g. US)'
  else
    DXB_CFG[_WIFI]=0
  fi

  if [[ -n ${DXB_CFG[CALLSIGN]:-} ]]; then
    [[ ${DXB_CFG[CALLSIGN]} =~ ^[A-Z0-9]{3,7}(-[0-9]{1,2})?$ ]] || _dxb_err CALLSIGN 'must look like N0CALL or N0CALL-10 (uppercase)'
  fi
  lat=${DXB_CFG[LATITUDE]:-}; lon=${DXB_CFG[LONGITUDE]:-}
  if [[ -n $lat || -n $lon ]]; then
    DXB_CFG[_BEACON]=1
    if [[ -z $lat || -z $lon ]]; then _dxb_err LATITUDE 'LATITUDE and LONGITUDE must be given together'; DXB_CFG[_BEACON]=0; fi
    [[ -z $lat ]] || _dxb_in_range "$lat" -90 90 || _dxb_err LATITUDE 'must be decimal degrees between -90 and 90'
    [[ -z $lon ]] || _dxb_in_range "$lon" -180 180 || _dxb_err LONGITUDE 'must be decimal degrees between -180 and 180'
  else
    DXB_CFG[_BEACON]=0
  fi
  _dxb_len_between "${DXB_CFG[BEACON_COMMENT]}" 0 43 || _dxb_err BEACON_COMMENT 'must be 43 characters or fewer'
  v=${DXB_CFG[BEACON_INTERVAL_MIN]}
  if [[ $v =~ ^[0-9]+$ ]] && (( 10#$v >= 1 && 10#$v <= 120 )); then DXB_CFG[_INTERVAL_S]=$(( 10#$v * 60 ))
  else _dxb_err BEACON_INTERVAL_MIN 'must be a whole number of minutes, 1-120'; DXB_CFG[_INTERVAL_S]=1800; fi
  [[ ${DXB_CFG[IGATE_SERVER]} =~ ^[A-Za-z0-9.-]+$ ]] || _dxb_err IGATE_SERVER 'must be a hostname'
  [[ ${DXB_CFG[WEBUI_USER]} =~ ^[A-Za-z0-9_.-]{1,32}$ ]] || _dxb_err WEBUI_USER 'must be 1-32 letters, digits, _ . -'
  [[ -n ${DXB_CFG[WEBUI_PASSWORD]:-} ]] || DXB_CFG[WEBUI_PASSWORD]=${DXB_CFG[PASSWORD]:-}
  v=${DXB_CFG[WEBUI_PASSWORD]}
  if [[ -n $v && $v != "$DXB_APPLIED" ]] && ! _dxb_len_between "$v" 8 100; then _dxb_err WEBUI_PASSWORD 'must be 8-100 characters'; fi
  case ${DXB_CFG[BEACON_SEND]} in
    is) DXB_CFG[_SEND_PATH]=is_only ;;
    rf|both) DXB_CFG[_SEND_PATH]=${DXB_CFG[BEACON_SEND]} ;;
    *) _dxb_err BEACON_SEND 'must be is, rf or both'; DXB_CFG[_SEND_PATH]=is_only ;;
  esac
  [[ ${DXB_CFG[BEACON_PATH]} =~ ^[A-Z0-9-]+(,[A-Z0-9-]+)*$ ]] || _dxb_err BEACON_PATH 'must be comma-separated path elements like WIDE1-1,WIDE2-1'
  v=${DXB_CFG[BEACON_SYMBOL]}
  if (( ${#v} == 2 )); then DXB_CFG[_SYMBOL_TABLE]=${v:0:1}; DXB_CFG[_SYMBOL]=${v:1:1}
  else _dxb_err BEACON_SYMBOL 'must be exactly two characters (table/overlay + symbol)'; DXB_CFG[_SYMBOL_TABLE]=R; DXB_CFG[_SYMBOL]='&'; fi
  case ${DXB_CFG[DIGIPEATER]} in off|fillin|wide) ;; *) _dxb_err DIGIPEATER 'must be off, fillin or wide' ;; esac
  for v in IGATE_RF_TO_IS IGATE_IS_TO_RF SERIAL_CONSOLE; do _dxb_is_onoff "${DXB_CFG[$v]}" || _dxb_err "$v" 'must be on or off'; done
  if [[ -n ${DXB_CFG[GRAYWOLF_VERSION]:-} ]]; then
    [[ ${DXB_CFG[GRAYWOLF_VERSION]} =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || _dxb_err GRAYWOLF_VERSION 'must look like v0.14.13'
  fi
  if [[ -n ${DXB_CFG[SSH_PUBKEY]:-} ]]; then
    [[ ${DXB_CFG[SSH_PUBKEY]} =~ ^(ssh-ed25519|ssh-rsa|ecdsa-sha2-nistp[0-9]+|sk-ssh-ed25519@openssh\.com)\ [A-Za-z0-9+/=]+ ]] || _dxb_err SSH_PUBKEY 'must be a single OpenSSH public key'
  fi
  (( ${#DXB_CFG_ERRORS[@]} == 0 ))
}

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
