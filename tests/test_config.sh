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
