#!/usr/bin/env bash
# shellcheck disable=SC1091,SC2034,SC2317
source "$DXB_LIB/common.sh"
source "$DXB_LIB/config.sh"
source "$DXB_LIB/gps.sh"
source "$DXB_LIB/radio.sh"
source "$DXB_LIB/update.sh"
source "$DXB_LIB/inifile.sh"
source "$DXB_LIB/apps.sh"
source "$DXB_ROOT/tests/fixtures/apps.sh"
source "$DXB_LIB/apps/js8call.sh"

# js_env: the two-radio program environment, radio2 on CAT PTT, call W0BTE, grid EM28qv, and the
# settings file copied from the real first-run fixture (what the program wrote on its first start)
js_env() {
  fx_apps_env
  js_deb_installed=0
  DXB_DPKG_QUERY=js_dpkgq
  DXB_CFG=([CALLSIGN]=W0BTE-7 [LATITUDE]=38.55 [LONGITUDE]=-92.25)
  local f; f=$(app_js8call_settings_file radio2)
  mkdir -p "${f%/*}"; cp "$DXB_ROOT/tests/fixtures/apps/js8call.ini" "$f"
}
# js_dpkgq: dpkg-query stand-in; the package is installed when js_deb_installed is 1
js_dpkgq() { if [[ ${js_deb_installed:-0} == 1 && ${*: -1} == js8call ]]; then echo "installed 1.0"; else return 1; fi; }
js_set_ptt() { DXB_RADIOS=$(jq -c --arg n "$1" --arg m "$2" '.radios[$n].ptt.method = $m' <<< "$DXB_RADIOS"); }

test_js8call_firstfill_then_wire_from_the_real_file() {
  js_env
  assert_ok app_js8call_firstfill radio2
  assert_ok app_js8call_wire radio2
  local f; f=$(app_js8call_settings_file radio2)
  assert_eq "$(dxb_ini_get "$f" Configuration MyCall)" "W0BTE"
  assert_eq "$(dxb_ini_get "$f" Configuration BroadcastToN1MM)" "true"
  assert_eq "$(dxb_ini_get "$f" Configuration SoundInName)" 'RADIO2'
  assert_eq "$(dxb_ini_get "$f" Configuration Rig)" "Hamlib NET rigctl"
  assert_eq "$(dxb_ini_get "$f" Configuration CATNetworkPort)" "127.0.0.1:4534"
  assert_contains "$(dxb_ini_get "$f" Configuration PTTMethod)" "PTT_method_CAT"
}
test_js8call_rewire_keeps_operator_keys() {
  js_env; app_js8call_firstfill radio2; app_js8call_wire radio2
  local f; f=$(app_js8call_settings_file radio2)
  dxb_ini_set "$f" Configuration MyCall K0XYZ; dxb_ini_set "$f" Common DialFreq 7074000
  cp "$f" "$TEST_TMP/before"
  js_set_ptt radio2 vox; app_js8call_wire radio2
  assert_eq "$(dxb_ini_get "$f" Configuration MyCall)" "K0XYZ"
  assert_eq "$(dxb_ini_get "$f" Common DialFreq)" "7074000"
  assert_eq "$(diff "$TEST_TMP/before" "$f" | grep -c '^[<>]')" "2"   # only PTTMethod changed
}
test_js8call_second_radio_gets_its_own_file_and_port() {
  js_env; js_deb_installed=1; app_js8call_wire radio1; app_js8call_wire radio2
  assert_eq "$(dxb_ini_get "$(app_js8call_settings_file radio1)" Configuration CATNetworkPort)" "127.0.0.1:4532"
  assert_eq "$(dxb_ini_get "$(app_js8call_settings_file radio2)" Configuration CATNetworkPort)" "127.0.0.1:4534"
  assert_eq "$(app_js8call_command radio2 1 | tr '\n' ' ')" "/usr/bin/JS8Call --rig-name=radio2 "
}
test_js8call_missing_file_is_created_with_only_our_keys() {
  js_env; rm -f "$(app_js8call_settings_file radio2)"
  app_js8call_firstfill radio2; app_js8call_wire radio2
  assert_eq "$(grep -c '=' "$(app_js8call_settings_file radio2)")" "12"
}
test_js8call_command_follows_how_it_was_installed() {
  js_env; js_deb_installed=0
  assert_eq "$(app_js8call_command radio2 1 | head -1)" "$DXB_APPS_OPT/js8call/squashfs-root/AppRun"
  js_deb_installed=1
  assert_eq "$(app_js8call_command radio2 1 | head -1)" "/usr/bin/JS8Call"
}
test_js8call_sound_names_are_the_private_pulse_devices() {
  js_env; app_js8call_wire radio2
  assert_eq "$(dxb_ini_get "$(app_js8call_settings_file radio2)" Configuration SoundOutName)" "RADIO2"
}
