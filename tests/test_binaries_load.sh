#!/usr/bin/env bash
# Every command that loads program modules defines the library functions those modules call. Each
# command is sourced in a clean process (env -i), so a library the shared test shell happens to
# have loaded cannot hide a missing one. (Prefix bl_.)
# shellcheck disable=SC2034,SC2016

BL_FUNCS="dxb_ini_set dxb_ini_get dxb_xml_set dxb_xml_get dxb_prefs_set dxb_prefs_get dxb_apps_installed dxb_apps_wiring dxb_apps_station dxb_app_load dxb_app_kind dxb_write_if_changed"

# bl_missing BINARY: the functions of BL_FUNCS that BINARY does not define once sourced (without running main)
bl_missing() {
  env -i PATH=/usr/bin:/bin DXB_LIB="$DXB_LIB" BIN="$DXB_ROOT/provision/bin/$1" FUNCS="$BL_FUNCS" \
    bash -c 'source "$BIN" || exit 99; for f in $FUNCS; do declare -F "$f" > /dev/null || echo "$f"; done' 2>&1
}

test_binaries_that_load_modules_define_the_libraries_modules_call() {
  local b out
  for b in dxberry-app dxberry-update dxberry-radio dxberry-provision; do
    out=$(bl_missing "$b")
    assert_eq "$out" ""
  done
}

# a module whose settings library is missing fails the step instead of reporting success
test_module_setters_fail_closed_when_the_writer_is_missing() {
  local m rc
  for m in wsjtx js8call qsstv fldigi; do
    rc=$(env -i PATH=/usr/bin:/bin DXB_LIB="$DXB_LIB" M="$m" bash -c \
      'source "$DXB_LIB/common.sh"; source "$DXB_LIB/apps/$M.sh"; "_dxb_${M}_set" /nonexistent/f a b c > /dev/null 2>&1; echo $?')
    assert_eq "$rc" "6"
  done
  rc=$(env -i PATH=/usr/bin:/bin DXB_LIB="$DXB_LIB" DXB_APPS_HOME="$TEST_TMP" bash -c \
    'source "$DXB_LIB/common.sh"; source "$DXB_LIB/apps/flrig.sh"; dxb_apps_wiring() { echo "{\"flrig_port\":12345}"; }; app_flrig_wire r > /dev/null 2>&1; echo $?')
  assert_eq "$rc" "6"
}
