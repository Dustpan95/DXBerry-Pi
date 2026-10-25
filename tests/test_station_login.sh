#!/usr/bin/env bash
# shellcheck disable=SC1091,SC2034,SC2317
# The login page's ?next= handling: only a path on this host is followed.

sl_next() { node "$DXB_ROOT/tests/station_login.js" "$DXB_ROOT/station/web/login.js" "$1"; }

test_station_login_follows_a_plain_path() {
  assert_eq "$(sl_next '/radios?x=1')" "/radios?x=1"
}

test_station_login_refuses_other_hosts_and_control_characters() {
  assert_eq "$(sl_next '//evil.example')" "/"
  assert_eq "$(sl_next $'/\t/evil.example')" "/"
  assert_eq "$(sl_next 'https://evil.example/x')" "/"
  assert_eq "$(sl_next $'/x\ny')" "/"
  assert_eq "$(sl_next '/\evil.example')" "/"
}
