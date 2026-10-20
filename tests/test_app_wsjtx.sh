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
source "$DXB_LIB/apps/wsjtx.sh"

# wx_env: the two-radio program environment, radio2 on CAT PTT, call W0BTE, grid EM28qv, and the
# settings file copied from the real first-run fixture (what the program wrote on its first start)
wx_env() {
  fx_apps_env
  DXB_CFG=([CALLSIGN]=W0BTE-7 [LATITUDE]=38.55 [LONGITUDE]=-92.25)
  local f; f=$(app_wsjtx_settings_file radio2)
  mkdir -p "${f%/*}"; cp "$DXB_ROOT/tests/fixtures/apps/wsjtx.ini" "$f"
}
wx_set_ptt() { DXB_RADIOS=$(jq -c --arg n "$1" --arg m "$2" '.radios[$n].ptt.method = $m' <<< "$DXB_RADIOS"); }

test_wsjtx_firstfill_then_wire_from_the_real_file() {
  wx_env
  assert_ok app_wsjtx_firstfill radio2
  assert_ok app_wsjtx_wire radio2
  local f; f=$(app_wsjtx_settings_file radio2)
  assert_eq "$(dxb_ini_get "$f" Configuration MyCall)" "W0BTE"
  assert_eq "$(dxb_ini_get "$f" Configuration BroadcastToN1MM)" "true"
  assert_eq "$(dxb_ini_get "$f" Configuration SoundInName)" '"plughw:CARD=RADIO2,DEV=0"'
  assert_eq "$(dxb_ini_get "$f" Configuration Rig)" "Hamlib NET rigctl"
  assert_eq "$(dxb_ini_get "$f" Configuration CATNetworkPort)" "127.0.0.1:4534"
  assert_contains "$(dxb_ini_get "$f" Configuration PTTMethod)" "PTT_method_CAT"
}
test_wsjtx_rewire_keeps_operator_keys() {
  wx_env; app_wsjtx_firstfill radio2; app_wsjtx_wire radio2
  local f; f=$(app_wsjtx_settings_file radio2)
  dxb_ini_set "$f" Configuration MyCall K0XYZ; dxb_ini_set "$f" Common DialFreq 7074000
  cp "$f" "$TEST_TMP/before"
  wx_set_ptt radio2 vox; app_wsjtx_wire radio2
  assert_eq "$(dxb_ini_get "$f" Configuration MyCall)" "K0XYZ"
  assert_eq "$(dxb_ini_get "$f" Common DialFreq)" "7074000"
  assert_eq "$(diff "$TEST_TMP/before" "$f" | grep -c '^[<>]')" "2"   # only PTTMethod changed
}
test_wsjtx_second_radio_gets_its_own_file_and_port() {
  wx_env; app_wsjtx_wire radio1; app_wsjtx_wire radio2
  assert_eq "$(dxb_ini_get "$(app_wsjtx_settings_file radio1)" Configuration CATNetworkPort)" "127.0.0.1:4532"
  assert_eq "$(dxb_ini_get "$(app_wsjtx_settings_file radio2)" Configuration CATNetworkPort)" "127.0.0.1:4534"
  assert_eq "$(app_wsjtx_command radio2 1 | tr '\n' ' ')" "wsjtx --rig-name=radio2 "
}
test_wsjtx_missing_file_is_created_with_only_our_keys() {
  wx_env; rm -f "$(app_wsjtx_settings_file radio2)"
  app_wsjtx_firstfill radio2; app_wsjtx_wire radio2
  assert_eq "$(grep -c '=' "$(app_wsjtx_settings_file radio2)")" "12"
}
