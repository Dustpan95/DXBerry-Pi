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
source "$DXB_LIB/apps/qsstv.sh"

# qs_env: the two-radio program environment, radio2 on CAT PTT, call W0BTE, grid EM28qv, and the
# settings file copied from the real first-run fixture (QSSTV 9.5.8)
qs_env() {
  fx_apps_env
  DXB_CFG=([CALLSIGN]=W0BTE-7 [LATITUDE]=38.88 [LONGITUDE]=-94.6)
  local f; f=$(app_qsstv_settings_file radio2)
  mkdir -p "${f%/*}"; cp "$DXB_ROOT/tests/fixtures/apps/qsstv_9.0.conf" "$f"
}
qs_set_ptt() { DXB_RADIOS=$(jq -c --arg n "$1" --arg m "$2" '.radios[$n].ptt.method = $m' <<< "$DXB_RADIOS"); }

test_qsstv_firstfill_then_wire_from_the_real_file() {
  qs_env
  assert_ok app_qsstv_firstfill radio2
  assert_ok app_qsstv_wire radio2
  local f; f=$(app_qsstv_settings_file radio2)
  [[ -n $f ]] || _fail "no settings path"
  assert_eq "$(dxb_ini_get "$f" PERSONAL callsign)" "W0BTE"
  assert_eq "$(dxb_ini_get "$f" PERSONAL locator)" "EM28qv"
  assert_eq "$(dxb_ini_get "$f" SOUND alsaSelected)" "false"
  assert_eq "$(dxb_ini_get "$f" SOUND pulseSelected)" "true"
  assert_eq "$(dxb_ini_get "$f" radio1 enableCAT)" "true"
  assert_eq "$(dxb_ini_get "$f" radio1 serialPort)" "127.0.0.1:4534"
  assert_eq "$(dxb_ini_get "$f" radio1 pttType)" "1"
  assert_eq "$(dxb_ini_get "$f" radio1 enableSerialPTT)" "false"
  assert_eq "$(dxb_ini_get "$f" radio1 enableXMLRPC)" "false"
}
test_qsstv_radio_model_keeps_its_four_leading_spaces() {
  qs_env; app_qsstv_wire radio2
  local f; f=$(app_qsstv_settings_file radio2)
  # QSSTV drops the first 5 characters of the list text (rigcontrol.cpp:350-352): "    2 Hamlib,NET rigctl"
  assert_eq "$(grep '^radioModel=' "$f")" 'radioModel="    2 Hamlib,NET rigctl"'
  assert_eq "$(dxb_ini_get "$f" radio1 radioModel)" '"    2 Hamlib,NET rigctl"'
  local v; v=$(dxb_ini_get "$f" radio1 radioModel); v=${v#\"}; v=${v%\"}
  assert_eq "${v:0:5}" "    2"
  assert_eq "${v:5}" " Hamlib,NET rigctl"
}
test_qsstv_ptt_none_when_the_radio_has_no_cat_ptt() {
  qs_env; qs_set_ptt radio2 vox; app_qsstv_wire radio2
  assert_eq "$(dxb_ini_get "$(app_qsstv_settings_file radio2)" radio1 pttType)" "0"
}
test_qsstv_firstfill_without_a_grid_leaves_it_alone() {
  qs_env; DXB_CFG=([CALLSIGN]=W0BTE-7 [LATITUDE]='' [LONGITUDE]='')
  assert_ok app_qsstv_firstfill radio2
  local f; f=$(app_qsstv_settings_file radio2)
  assert_eq "$(dxb_ini_get "$f" PERSONAL locator)" "NOLOCATOR"
  assert_eq "$(dxb_ini_get "$f" PERSONAL callsign)" "W0BTE"
}
test_qsstv_rewire_keeps_operator_keys() {
  qs_env; app_qsstv_firstfill radio2; app_qsstv_wire radio2
  local f; f=$(app_qsstv_settings_file radio2)
  assert_ok dxb_ini_set "$f" PERSONAL qth Kansas
  assert_ok dxb_ini_set "$f" TX imageQuality 7
  cp "$f" "$TEST_TMP/before"
  qs_set_ptt radio2 vox; app_qsstv_wire radio2
  assert_eq "$(dxb_ini_get "$f" PERSONAL qth)" "Kansas"
  assert_eq "$(dxb_ini_get "$f" TX imageQuality)" "7"
  assert_eq "$(diff "$TEST_TMP/before" "$f" | grep -c '^[<>]')" "2"   # only pttType changed
}
test_qsstv_missing_file_is_created_with_only_our_keys() {
  qs_env; rm -f "$(app_qsstv_settings_file radio2)"; rm -rf "$DXB_APPS_HOME/.config"
  assert_ok app_qsstv_firstfill radio2; assert_ok app_qsstv_wire radio2
  assert_eq "$(grep -c "=" "$(app_qsstv_settings_file radio2)")" "10"
}
test_qsstv_is_single_copy() {
  qs_env
  assert_eq "$(app_qsstv_multi)" "no"
  assert_eq "$(app_qsstv_audio)" "pulse"
  assert_eq "$(app_qsstv_needs_service_restart)" "no"
  assert_ok app_qsstv_unwire radio2
  assert_eq "$(app_qsstv_command radio2 0 | tr '\n' ' ')" "qsstv "
}
