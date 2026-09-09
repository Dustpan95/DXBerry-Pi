#!/usr/bin/env bash
# shellcheck disable=SC1091
source "$DXB_LIB/config.sh"

write_cfg() { printf '%s\n' "$@" > "$TEST_TMP/dxberry.txt"; }

test_config_load_parses_keys_quotes_crlf_and_comments() {
  printf 'HOSTNAME=pi-one\r\n# comment\r\n\r\n  PASSWORD = "spaces ok"  \r\nCALLSIGN=N0CALL-2\n' > "$TEST_TMP/dxberry.txt"
  assert_ok dxb_config_load "$TEST_TMP/dxberry.txt"
  assert_eq "${DXB_CFG[HOSTNAME]}" "pi-one"
  assert_eq "${DXB_CFG[PASSWORD]}" "spaces ok"
  assert_eq "${DXB_CFG[CALLSIGN]}" "N0CALL-2"
  assert_eq "${DXB_CFG_LINES[CALLSIGN]}" "5"
  assert_eq "${#DXB_CFG_ERRORS[@]}" "0"
}

# Windows Notepad saves UTF-8 with a byte-order mark. It lands on line 1 - a comment in the
# shipped example - and used to turn the whole file into "expected KEY=value".
test_config_load_strips_a_utf8_bom_from_line_1() {
  export DXB_ZONEINFO_DIR=$TEST_TMP/no-such-dir
  { printf '\xef\xbb\xbf'; sed 's/^PASSWORD=.*/PASSWORD=examplepass/' "$DXB_ROOT/boot/dxberry.txt.example"; } > "$TEST_TMP/dxberry.txt"
  assert_ok dxb_config_load "$TEST_TMP/dxberry.txt"
  assert_ok dxb_config_validate
  assert_eq "${#DXB_CFG_ERRORS[@]}" "0"
  assert_eq "${DXB_CFG[PASSWORD]}" "examplepass"
  # A BOM in front of a key must not become part of the key either.
  printf '\xef\xbb\xbfPASSWORD=secretpass\n' > "$TEST_TMP/dxberry.txt"
  assert_ok dxb_config_load "$TEST_TMP/dxberry.txt"
  assert_ok dxb_config_validate
  assert_eq "${DXB_CFG[PASSWORD]}" "secretpass"
}

test_config_load_warns_on_unknown_key_and_ignores_reserved_prefix() {
  write_cfg 'PASSWORD=secretpass' 'FOO=bar' 'PAT_MYCALL=X'
  dxb_config_load "$TEST_TMP/dxberry.txt"
  assert_eq "${#DXB_CFG_WARNINGS[@]}" "1"
  assert_contains "${DXB_CFG_WARNINGS[0]}" "line 2: unknown key 'FOO'"
  assert_eq "${DXB_CFG[PAT_MYCALL]:-unset}" "unset"
}

test_config_load_reports_malformed_lines() {
  write_cfg 'PASSWORD=secretpass' 'this is not a key' 'lower=case'
  dxb_config_load "$TEST_TMP/dxberry.txt"
  assert_eq "${#DXB_CFG_ERRORS[@]}" "2"
  assert_contains "${DXB_CFG_ERRORS[0]}" "line 2: expected KEY=value"
  assert_contains "${DXB_CFG_ERRORS[1]}" "line 3: invalid key 'lower'"
}

test_config_load_missing_file() {
  assert_fails dxb_config_load "$TEST_TMP/nope.txt"
  assert_contains "${DXB_CFG_ERRORS[0]}" "cannot read"
}

load_and_validate() { dxb_config_load "$TEST_TMP/dxberry.txt"; dxb_config_validate; }
errors_text() { printf '%s\n' "${DXB_CFG_ERRORS[@]}"; }

test_validate_minimal_valid_config_applies_defaults() {
  export DXB_ZONEINFO_DIR=$TEST_TMP/no-such-dir
  write_cfg 'PASSWORD=secretpass'
  assert_ok load_and_validate
  assert_eq "${DXB_CFG[HOSTNAME]}" "dxberry-pi"
  assert_eq "${DXB_CFG[TIMEZONE]}" "UTC"
  assert_eq "${DXB_CFG[_MODE]}" "dhcp"
  assert_eq "${DXB_CFG[_WIFI]}" "0"
  assert_eq "${DXB_CFG[_BEACON]}" "0"
  assert_eq "${DXB_CFG[WEBUI_USER]}" "admin"
  assert_eq "${DXB_CFG[WEBUI_PASSWORD]}" "secretpass"
  assert_eq "${DXB_CFG[IGATE_SERVER]}" "rotate.aprs2.net"
  assert_eq "${DXB_CFG[_SEND_PATH]}" "is_only"
  assert_eq "${DXB_CFG[_SYMBOL_TABLE]}" "R"
  assert_eq "${DXB_CFG[_SYMBOL]}" "&"
  assert_eq "${DXB_CFG[_INTERVAL_S]}" "1800"
  assert_eq "${DXB_CFG[SERIAL_CONSOLE]}" "off"
}

test_validate_password_required_and_length() {
  export DXB_ZONEINFO_DIR=$TEST_TMP/no-such-dir
  write_cfg 'HOSTNAME=x'
  assert_fails load_and_validate
  assert_contains "$(errors_text)" "PASSWORD is required"
  write_cfg 'PASSWORD=short'
  assert_fails load_and_validate
  assert_contains "$(errors_text)" "line 1: PASSWORD must be 8-100 characters"
  write_cfg 'PASSWORD=<applied>'
  assert_ok load_and_validate
}

test_validate_static_ip_rules() {
  export DXB_ZONEINFO_DIR=$TEST_TMP/no-such-dir
  write_cfg 'PASSWORD=secretpass' 'STATIC_IP=192.168.1.90/24' 'GATEWAY=192.168.1.1'
  assert_ok load_and_validate
  assert_eq "${DXB_CFG[_MODE]}" "static"
  assert_eq "${DXB_CFG[_IP]}" "192.168.1.90"
  assert_eq "${DXB_CFG[_PREFIX]}" "24"
  assert_eq "${DXB_CFG[DNS]}" "192.168.1.1"
  write_cfg 'PASSWORD=secretpass' 'STATIC_IP=192.168.1.90/24'
  assert_fails load_and_validate
  assert_contains "$(errors_text)" "GATEWAY is required when STATIC_IP is set"
  write_cfg 'PASSWORD=secretpass' 'STATIC_IP=192.168.1.90/24' 'GATEWAY=10.0.0.1'
  assert_fails load_and_validate
  assert_contains "$(errors_text)" "GATEWAY is not inside 192.168.1.90/24"
  write_cfg 'PASSWORD=secretpass' 'STATIC_IP=192.168.1.90' 'GATEWAY=192.168.1.1'
  assert_ok load_and_validate
  assert_eq "${DXB_CFG[STATIC_IP]}" "192.168.1.90/24"
  write_cfg 'PASSWORD=secretpass' 'STATIC_IP=192.168.1.90/24' 'GATEWAY=192.168.1.1' 'DNS=1.1.1.1 999.1.1.1'
  assert_fails load_and_validate
  assert_contains "$(errors_text)" "DNS '999.1.1.1' is not a valid IPv4 address"
}

test_validate_wifi_rules() {
  export DXB_ZONEINFO_DIR=$TEST_TMP/no-such-dir
  write_cfg 'PASSWORD=secretpass' 'WIFI_SSID=Home'
  assert_fails load_and_validate
  assert_contains "$(errors_text)" "WIFI_PASSWORD is required when WIFI_SSID is set"
  assert_contains "$(errors_text)" "WIFI_COUNTRY must be a two-letter uppercase country code"
  write_cfg 'PASSWORD=secretpass' 'WIFI_SSID=Home' 'WIFI_PASSWORD=wifipass1' 'WIFI_COUNTRY=US'
  assert_ok load_and_validate
  assert_eq "${DXB_CFG[_WIFI]}" "1"
  write_cfg 'PASSWORD=secretpass' 'WIFI_SSID=Home' 'WIFI_PASSWORD=<applied>' 'WIFI_COUNTRY=US'
  assert_ok load_and_validate
}

test_validate_station_and_beacon_rules() {
  export DXB_ZONEINFO_DIR=$TEST_TMP/no-such-dir
  write_cfg 'PASSWORD=secretpass' 'CALLSIGN=n0call' 'LATITUDE=37.1' 'BEACON_INTERVAL_MIN=0' 'BEACON_SEND=maybe' 'DIGIPEATER=yes' 'BEACON_SYMBOL=abc' 'GRAYWOLF_VERSION=latest' 'IGATE_IS_TO_RF=yes'
  assert_fails load_and_validate
  local e; e=$(errors_text)
  assert_contains "$e" "CALLSIGN must look like N0CALL or N0CALL-10"
  assert_contains "$e" "LATITUDE and LONGITUDE must be given together"
  assert_contains "$e" "BEACON_INTERVAL_MIN must be a whole number of minutes, 1-120"
  assert_contains "$e" "BEACON_SEND must be is, rf or both"
  assert_contains "$e" "DIGIPEATER must be off, fillin or wide"
  assert_contains "$e" "BEACON_SYMBOL must be exactly two characters"
  assert_contains "$e" "GRAYWOLF_VERSION must look like v0.14.13"
  assert_contains "$e" "IGATE_IS_TO_RF must be on or off"
  write_cfg 'PASSWORD=secretpass' 'CALLSIGN=N0CALL-2' 'LATITUDE=37.145833' 'LONGITUDE=-101.375' 'BEACON_INTERVAL_MIN=10' 'BEACON_SEND=both' 'BEACON_SYMBOL=/#' 'DIGIPEATER=wide' 'GRAYWOLF_VERSION=v0.14.13'
  assert_ok load_and_validate
  assert_eq "${DXB_CFG[_BEACON]}" "1"
  assert_eq "${DXB_CFG[_INTERVAL_S]}" "600"
  assert_eq "${DXB_CFG[_SEND_PATH]}" "both"
  assert_eq "${DXB_CFG[_SYMBOL_TABLE]}" "/"
  assert_eq "${DXB_CFG[_SYMBOL]}" "#"
}

test_validate_timezone_checked_against_zoneinfo_when_present() {
  mkdir -p "$TEST_TMP/zi/America"; : > "$TEST_TMP/zi/America/Chicago"
  export DXB_ZONEINFO_DIR=$TEST_TMP/zi
  write_cfg 'PASSWORD=secretpass' 'TIMEZONE=America/Chicago'
  assert_ok load_and_validate
  write_cfg 'PASSWORD=secretpass' 'TIMEZONE=Mars/Phobos'
  assert_fails load_and_validate
  assert_contains "$(errors_text)" "TIMEZONE 'Mars/Phobos' is not a known time zone"
}

test_validate_hostname_and_ssh_key() {
  export DXB_ZONEINFO_DIR=$TEST_TMP/no-such-dir
  write_cfg 'PASSWORD=secretpass' 'HOSTNAME=Bad_Name' 'SSH_PUBKEY=not a key'
  assert_fails load_and_validate
  assert_contains "$(errors_text)" "HOSTNAME must be lowercase letters, digits and hyphens"
  assert_contains "$(errors_text)" "SSH_PUBKEY must be a single OpenSSH public key"
  write_cfg 'PASSWORD=secretpass' 'HOSTNAME=dx-berry-2' 'SSH_PUBKEY=ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExample+key/here= me@host'
  assert_ok load_and_validate
}

test_config_prefix_to_mask() {
  assert_eq "$(dxb_prefix_to_mask 8)" "255.0.0.0"
  assert_eq "$(dxb_prefix_to_mask 16)" "255.255.0.0"
  assert_eq "$(dxb_prefix_to_mask 24)" "255.255.255.0"
  assert_eq "$(dxb_prefix_to_mask 25)" "255.255.255.128"
  assert_eq "$(dxb_prefix_to_mask 30)" "255.255.255.252"
}

test_print_masked_hides_secrets() {
  export DXB_ZONEINFO_DIR=$TEST_TMP/no-such-dir
  write_cfg 'PASSWORD=secretpass' 'WIFI_SSID=Home' 'WIFI_PASSWORD=<applied>' 'WIFI_COUNTRY=US'
  load_and_validate
  local out; out=$(dxb_config_print_masked)
  assert_contains "$out" "PASSWORD=********"
  assert_contains "$out" "WIFI_PASSWORD=<applied>"
  assert_contains "$out" "WEBUI_PASSWORD=********"
  assert_not_contains "$out" "secretpass"
}

cfg_from() { printf '%s\n' "$@" > "$TEST_TMP/dxberry.txt"; dxb_config_load "$TEST_TMP/dxberry.txt"; export DXB_ZONEINFO_DIR=$TEST_TMP/nozone; }

test_config_bare_static_ip_defaults_to_24() {
  cfg_from 'PASSWORD=examplepass' 'STATIC_IP=10.0.0.90' 'GATEWAY=10.0.0.1'
  assert_ok dxb_config_validate
  assert_eq "${DXB_CFG[STATIC_IP]}" "10.0.0.90/24"
  assert_eq "${DXB_CFG[_IP]}" "10.0.0.90"
  assert_eq "${DXB_CFG[_PREFIX]}" "24"
}

test_config_static_ip_with_prefix_unchanged() {
  cfg_from 'PASSWORD=examplepass' 'STATIC_IP=10.0.0.90/16' 'GATEWAY=10.0.1.1'
  assert_ok dxb_config_validate
  assert_eq "${DXB_CFG[STATIC_IP]}" "10.0.0.90/16"
  assert_eq "${DXB_CFG[_PREFIX]}" "16"
}

test_config_static_ip_garbage_still_rejected() {
  cfg_from 'PASSWORD=examplepass' 'STATIC_IP=10.0.0' 'GATEWAY=10.0.0.1'
  assert_fails dxb_config_validate
  assert_contains "${DXB_CFG_ERRORS[*]}" "STATIC_IP must be an IPv4 address"
}

test_config_gps_defaults() {
  cfg_from 'PASSWORD=examplepass'
  assert_ok dxb_config_validate
  assert_eq "${DXB_CFG[GPS_DEVICE]}" "auto"; assert_eq "${DXB_CFG[GPS_BAUD]}" "9600"; assert_eq "${DXB_CFG[GPS_PPS]}" ""
  assert_eq "${DXB_CFG[_GPS]}" "1"; assert_eq "${DXB_CFG[_GPS_PATH]}" ""
}

test_config_gps_uart_and_pps() {
  cfg_from 'PASSWORD=examplepass' 'GPS_DEVICE=uart' 'GPS_PPS=18'
  assert_ok dxb_config_validate
  assert_eq "${DXB_CFG[_GPS_PATH]}" "/dev/ttyAMA0"; assert_eq "${DXB_CFG[GPS_PPS]}" "18"
}

test_config_gps_none_and_explicit_path() {
  cfg_from 'PASSWORD=examplepass' 'GPS_DEVICE=none'
  assert_ok dxb_config_validate; assert_eq "${DXB_CFG[_GPS]}" "0"
  cfg_from 'PASSWORD=examplepass' 'GPS_DEVICE=/dev/ttyUSB3' 'GPS_BAUD=4800'
  assert_ok dxb_config_validate; assert_eq "${DXB_CFG[_GPS_PATH]}" "/dev/ttyUSB3"
}

test_config_gps_rejections() {
  cfg_from 'PASSWORD=examplepass' 'GPS_DEVICE=uart' 'SERIAL_CONSOLE=on'
  assert_fails dxb_config_validate; assert_contains "${DXB_CFG_ERRORS[*]}" "GPS_DEVICE uart needs SERIAL_CONSOLE=off"
  cfg_from 'PASSWORD=examplepass' 'GPS_DEVICE=ttyUSB0'
  assert_fails dxb_config_validate; assert_contains "${DXB_CFG_ERRORS[*]}" "GPS_DEVICE must be auto, none, uart or a /dev/tty path"
  cfg_from 'PASSWORD=examplepass' 'GPS_BAUD=1234'
  assert_fails dxb_config_validate; assert_contains "${DXB_CFG_ERRORS[*]}" "GPS_BAUD must be one of"
  cfg_from 'PASSWORD=examplepass' 'GPS_PPS=40'
  assert_fails dxb_config_validate; assert_contains "${DXB_CFG_ERRORS[*]}" "GPS_PPS must be a BCM GPIO number 0-27"
}

test_config_gps_keys_are_known_not_reserved() {
  cfg_from 'PASSWORD=examplepass' 'GPS_FOO=1'
  assert_ok dxb_config_validate
  assert_contains "${DXB_CFG_WARNINGS[*]}" "GPS_FOO"
}
