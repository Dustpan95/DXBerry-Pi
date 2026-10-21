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
source "$DXB_LIB/apps/fldigi.sh"

# fd_env: the two-radio program environment, radio2 on CAT PTT, call W0BTE, grid EM28qv, and the
# settings file copied from the real fixture (what fldigi wrote on its first start); no logbook
fd_env() {
  fx_apps_env
  unset -f dxb_logbook_fldigi_values
  DXB_CFG=([CALLSIGN]=W0BTE-7 [LATITUDE]=38.88 [LONGITUDE]=-94.6)
  local f; f=$(app_fldigi_settings_file radio2)
  mkdir -p "${f%/*}"; cp "$DXB_ROOT/tests/fixtures/apps/fldigi_def.xml" "$f"
}
# fd_logbook: dxb_logbook_fldigi_values answers $FD_LOGBOOK (every test that uses it ends with fd_unlogbook)
fd_logbook() { dxb_logbook_fldigi_values() { printf '%s\n' "$FD_LOGBOOK"; }; }
fd_unlogbook() { unset -f dxb_logbook_fldigi_values; }
fd_set_wiring() { DXB_RADIOS=$(jq -c --arg n "$1" --arg k "$2" --arg v "$3" '.radios[$n] |= (if $k == "ptt" then .ptt.method = $v else .rig.control = $v end)' <<< "$DXB_RADIOS"); }

test_fldigi_firstfill_and_wire_touch_only_our_keys() {
  fd_env
  local f; f=$(app_fldigi_settings_file radio2)
  [[ -n $f ]] || _fail "no settings path"
  cp "$f" "$TEST_TMP/before"
  assert_ok app_fldigi_firstfill radio2
  assert_ok app_fldigi_wire radio2
  assert_eq "$(dxb_xml_get "$f" MYCALL)" "W0BTE"
  assert_eq "$(dxb_xml_get "$f" MYLOC)" "EM28qv"
  assert_eq "$(dxb_xml_get "$f" AUDIOIO)" "2"
  assert_eq "$(dxb_xml_get "$f" PULSESERVER)" ""
  assert_eq "$(dxb_xml_get "$f" CHKUSEHAMLIBIS)" "1"
  assert_eq "$(dxb_xml_get "$f" CHKUSEXMLRPCIS)" "0"
  assert_eq "$(dxb_xml_get "$f" FLRIG_AUTO_ENABLE)" "0"
  assert_eq "$(dxb_xml_get "$f" HAMRIGMODEL)" "2"
  assert_eq "$(dxb_xml_get "$f" HAMRIGDEVICE)" "127.0.0.1:4534"
  assert_eq "$(dxb_xml_get "$f" HAMLIBCMDPTT)" "1"
  assert_eq "$(dxb_xml_get "$f" RTSPTT)" "0"
  assert_eq "$(dxb_xml_get "$f" DTRPTT)" "0"
  assert_eq "$(dxb_xml_get "$f" XMLRPC_PORT)" "7363"
  assert_eq "$(dxb_xml_get "$f" ARQ_PORT)" "7323"
  assert_eq "$(dxb_xml_get "$f" ENCLOUDLOG)" "0"                  # no logbook set: untouched
  # the diff touches nothing but those keys' lines
  local changed; changed=$(diff "$TEST_TMP/before" "$f" | grep '^>' | sed 's/^> *<\([A-Z0-9_]*\)>.*/\1/' | sort | tr '\n' ' ')
  assert_eq "$changed" "ARQ_PORT AUDIOIO CHKUSEHAMLIBIS HAMLIBCMDPTT HAMRIGDEVICE HAMRIGMODEL MYCALL MYLOC XMLRPC_PORT "
  assert_eq "$(diff "$TEST_TMP/before" "$f" | grep -c '^<')" "$(diff "$TEST_TMP/before" "$f" | grep -c '^>')"
}
test_fldigi_rewire_keeps_operator_keys() {
  fd_env; app_fldigi_firstfill radio2; app_fldigi_wire radio2
  local f; f=$(app_fldigi_settings_file radio2)
  [[ -n $f ]] || _fail "no settings path"
  assert_ok dxb_xml_set "$f" MYQTH "Lenexa"
  assert_ok dxb_xml_set "$f" HAMLIBCMDPTT 0                        # a wiring key: restored
  assert_ok app_fldigi_wire radio2
  assert_eq "$(dxb_xml_get "$f" MYQTH)" "Lenexa"
  assert_eq "$(dxb_xml_get "$f" HAMLIBCMDPTT)" "1"
  fd_set_wiring radio2 ptt vox; app_fldigi_wire radio2
  assert_eq "$(dxb_xml_get "$f" HAMLIBCMDPTT)" "0"
  assert_eq "$(dxb_xml_get "$f" MYQTH)" "Lenexa"
}
test_fldigi_flrig_radio_uses_flrig_not_hamlib() {
  fd_env; fd_set_wiring radio2 control flrig
  assert_ok app_fldigi_wire radio2
  local f; f=$(app_fldigi_settings_file radio2)
  [[ -n $f ]] || _fail "no settings path"
  assert_eq "$(dxb_xml_get "$f" CHKUSEHAMLIBIS)" "0"
  assert_eq "$(dxb_xml_get "$f" FLRIG_AUTO_ENABLE)" "1"
  assert_eq "$(dxb_xml_get "$f" FLRIG_IP_ADDRESS)" "127.0.0.1"
  assert_eq "$(dxb_xml_get "$f" FLRIG_IP_PORT)" "12346"
  assert_eq "$(dxb_xml_get "$f" HAMRIGDEVICE)" "/dev/ttyS0"       # hamlib keys left alone
  assert_eq "$(dxb_xml_get "$f" HAMLIBCMDPTT)" "0"
}
test_fldigi_two_radios_get_their_own_ports() {
  fd_env
  app_fldigi_wire radio1; app_fldigi_wire radio2
  assert_eq "$(dxb_xml_get "$(app_fldigi_settings_file radio1)" XMLRPC_PORT)" "7362"
  assert_eq "$(dxb_xml_get "$(app_fldigi_settings_file radio2)" XMLRPC_PORT)" "7363"
  assert_eq "$(dxb_xml_get "$(app_fldigi_settings_file radio1)" ARQ_PORT)" "7322"
  assert_eq "$(dxb_xml_get "$(app_fldigi_settings_file radio2)" ARQ_PORT)" "7323"
  assert_eq "$(dxb_xml_get "$(app_fldigi_settings_file radio1)" HAMRIGDEVICE)" "127.0.0.1:4532"
  assert_eq "$(app_fldigi_command radio2 1 | tr '\n' ' ')" "fldigi --config-dir $DXB_APPS_HOME/.fldigi-radio2/ "
  assert_eq "$(app_fldigi_audio) $(app_fldigi_multi) $(app_fldigi_label) $(app_fldigi_needs_service_restart)" "pulse yes fldigi no"
  assert_ok app_fldigi_unwire radio2
}
test_fldigi_missing_file_is_created() {
  fd_env; rm -rf "$(dirname "$(app_fldigi_settings_file radio2)")"
  assert_ok app_fldigi_firstfill radio2; assert_ok app_fldigi_wire radio2
  assert_eq "$(dxb_xml_get "$(app_fldigi_settings_file radio2)" MYCALL)" "W0BTE"
}
test_fldigi_firstfill_writes_the_logbook_and_records_only_hashes() {
  fd_env; fd_logbook; FD_LOGBOOK='{"url":"https://a.example/","key":"k1","station_id":"3"}'
  assert_ok app_fldigi_firstfill radio2
  local f rec; f=$(app_fldigi_settings_file radio2); rec=${f%/*}/dxberry-logbook.json
  assert_eq "$(dxb_xml_get "$f" ENCLOUDLOG)" "1"
  assert_eq "$(dxb_xml_get "$f" CLOUDLOG_API_URL)" "https://a.example/"
  assert_eq "$(dxb_xml_get "$f" CLOUDLOG_API_KEY)" "k1"
  assert_eq "$(dxb_xml_get "$f" CLOUDLOG_STATION_ID)" "3"
  assert_eq "$(stat -c %a "$rec")" "600"
  assert_eq "$(jq -r 'keys | join(" ")' "$rec")" "CLOUDLOG_API_KEY CLOUDLOG_API_URL CLOUDLOG_STATION_ID ENCLOUDLOG"
  assert_eq "$(jq -r .CLOUDLOG_STATION_ID "$rec")" "$(printf %s 3 | sha256sum | cut -d' ' -f1)"
  assert_file_not_contains "$rec" "k1"
  assert_file_not_contains "$rec" "a.example"
  [[ ! -e $DXB_APPS_STATE ]] || _fail "something was written under DXB_APPS_STATE"
  fd_unlogbook
}
test_fldigi_logbook_follow_keeps_what_the_operator_changed() {
  fd_env; fd_logbook; FD_LOGBOOK='{"url":"https://a.example/","key":"k1","station_id":"1"}'
  app_fldigi_firstfill radio2
  local f; f=$(app_fldigi_settings_file radio2)
  dxb_xml_set "$f" CLOUDLOG_STATION_ID 7                         # the operator picked another station
  FD_LOGBOOK='{"url":"https://b.example/","key":"k2","station_id":"2"}'
  assert_contains "$(app_fldigi_logbook_follow radio2)" "kept CLOUDLOG_STATION_ID (changed in fldigi)"
  assert_eq "$(dxb_xml_get "$f" CLOUDLOG_API_URL)" "https://b.example/"
  assert_eq "$(dxb_xml_get "$f" CLOUDLOG_API_KEY)" "k2"
  assert_eq "$(dxb_xml_get "$f" CLOUDLOG_STATION_ID)" "7"
  assert_not_contains "$(app_fldigi_logbook_follow radio2)" "CLOUDLOG_API_URL"
  assert_file_not_contains "${f%/*}/dxberry-logbook.json" "k2"
  assert_file_not_contains "${f%/*}/dxberry-logbook.json" "k1"
  fd_unlogbook
}
test_fldigi_logbook_follow_without_a_logbook_or_file_does_nothing() {
  fd_env; fd_logbook; FD_LOGBOOK='{}'
  local f; f=$(app_fldigi_settings_file radio2)
  cp "$f" "$TEST_TMP/before"
  assert_eq "$(app_fldigi_logbook_follow radio2)" ""
  assert_ok cmp "$TEST_TMP/before" "$f"
  rm -f "$f"; FD_LOGBOOK='{"url":"https://a.example/","key":"k1","station_id":"1"}'
  assert_ok app_fldigi_logbook_follow radio2
  [[ ! -e $f ]] || _fail "follow created the settings file"
  fd_unlogbook
}
test_fldigi_logbook_key_is_never_in_an_argument() {
  fd_env; fd_logbook; FD_LOGBOOK='{"url":"https://a.example/","key":"SECRETKEY99","station_id":"1"}'
  local real; real=$(command -v sha256sum); mkdir -p "$TEST_TMP/shim"
  printf '#!/bin/bash\nprintf "%%s\\n" "$*" >> "%s/argv"\nexec %s "$@"\n' "$TEST_TMP" "$real" > "$TEST_TMP/shim/sha256sum"
  chmod 755 "$TEST_TMP/shim/sha256sum"
  PATH="$TEST_TMP/shim:$PATH" app_fldigi_firstfill radio2
  [[ -f $TEST_TMP/argv ]] || : > "$TEST_TMP/argv"
  assert_file_not_contains "$TEST_TMP/argv" "SECRETKEY99"
  assert_eq "$(dxb_xml_get "$(app_fldigi_settings_file radio2)" CLOUDLOG_API_KEY)" "SECRETKEY99"
  fd_unlogbook
}
test_fldigi_cleared_logbook_stops_posting_and_drops_the_key() {
  fd_env; fd_logbook; FD_LOGBOOK='{"url":"https://a.example/","key":"k1","station_id":"4"}'
  app_fldigi_firstfill radio2
  local f rec; f=$(app_fldigi_settings_file radio2); rec=${f%/*}/dxberry-logbook.json
  FD_LOGBOOK='{}'
  assert_eq "$(app_fldigi_logbook_follow radio2)" ""
  assert_eq "$(dxb_xml_get "$f" ENCLOUDLOG)" "0"
  assert_eq "$(dxb_xml_get "$f" CLOUDLOG_API_URL)" ""
  assert_eq "$(dxb_xml_get "$f" CLOUDLOG_API_KEY)" ""
  assert_eq "$(dxb_xml_get "$f" CLOUDLOG_STATION_ID)" "4"
  assert_file_not_contains "$f" "k1"
  assert_file_not_contains "$rec" "k1"
  assert_eq "$(jq -r .ENCLOUDLOG "$rec")" "$(printf %s 0 | sha256sum | cut -d' ' -f1)"
  assert_eq "$(app_fldigi_logbook_follow radio2)" ""             # and it stays quiet
  fd_unlogbook
}
test_fldigi_cleared_logbook_keeps_an_operators_own_url() {
  fd_env; fd_logbook; FD_LOGBOOK='{"url":"https://a.example/","key":"k1","station_id":"1"}'
  app_fldigi_firstfill radio2
  local f; f=$(app_fldigi_settings_file radio2)
  dxb_xml_set "$f" CLOUDLOG_API_URL "https://mine.example/"
  FD_LOGBOOK='{}'
  assert_eq "$(app_fldigi_logbook_follow radio2)" "kept CLOUDLOG_API_URL (changed in fldigi)"
  assert_eq "$(dxb_xml_get "$f" CLOUDLOG_API_URL)" "https://mine.example/"
  assert_eq "$(dxb_xml_get "$f" ENCLOUDLOG)" "0"
  assert_eq "$(dxb_xml_get "$f" CLOUDLOG_API_KEY)" ""
  fd_unlogbook
}
test_fldigi_failed_write_partway_keeps_the_record_true() {
  fd_env; fd_logbook; FD_LOGBOOK='{"url":"https://a.example/","key":"k1","station_id":"1"}'
  app_fldigi_firstfill radio2
  local f rec; f=$(app_fldigi_settings_file radio2); rec=${f%/*}/dxberry-logbook.json
  FD_LOGBOOK='{}'
  local saved; saved=$(declare -f dxb_xml_set)
  dxb_xml_set() { [[ $2 == CLOUDLOG_API_URL ]] && return 6; _dxb_xml_set_real "$@"; }
  eval "${saved/dxb_xml_set/_dxb_xml_set_real}"
  app_fldigi_logbook_follow radio2 2> /dev/null; assert_eq "$?" "6"
  eval "$saved"; unset -f _dxb_xml_set_real
  assert_eq "$(dxb_xml_get "$f" ENCLOUDLOG)" "0"
  assert_eq "$(jq -r .ENCLOUDLOG "$rec")" "$(printf %s 0 | sha256sum | cut -d' ' -f1)"
  assert_eq "$(app_fldigi_logbook_follow radio2)" ""             # nothing reported as the operator's
  assert_eq "$(dxb_xml_get "$f" CLOUDLOG_API_URL)" ""
  fd_unlogbook
}
test_fldigi_corrupt_record_is_warned_about_and_rewritten() {
  fd_env; fd_logbook; FD_LOGBOOK='{"url":"https://a.example/","key":"k1","station_id":"1"}'
  local f rec; f=$(app_fldigi_settings_file radio2); rec=${f%/*}/dxberry-logbook.json
  echo 'not json {' > "$rec"
  local out err; out=$(app_fldigi_logbook_follow radio2 2> "$TEST_TMP/err")
  assert_eq "$out" ""
  assert_contains "$(cat "$TEST_TMP/err")" "not a valid record"
  assert_eq "$(grep -c . "$TEST_TMP/err")" "1"
  assert_eq "$(dxb_xml_get "$f" CLOUDLOG_API_URL)" "https://a.example/"
  assert_ok jq -e 'keys | length == 4' "$rec" > /dev/null
  fd_unlogbook
}
test_fldigi_follow_records_a_value_the_operator_already_set_to_the_new_one() {
  fd_env; fd_logbook; FD_LOGBOOK='{"url":"https://a.example/","key":"k1","station_id":"1"}'
  app_fldigi_firstfill radio2
  local f; f=$(app_fldigi_settings_file radio2)
  dxb_xml_set "$f" CLOUDLOG_API_URL "https://b.example/"          # the operator typed the new address
  FD_LOGBOOK='{"url":"https://b.example/","key":"k1","station_id":"1"}'
  assert_eq "$(app_fldigi_logbook_follow radio2)" ""
  FD_LOGBOOK='{"url":"https://c.example/","key":"k1","station_id":"1"}'
  assert_eq "$(app_fldigi_logbook_follow radio2)" ""             # now DXBerry's again, so it follows
  assert_eq "$(dxb_xml_get "$f" CLOUDLOG_API_URL)" "https://c.example/"
  fd_unlogbook
}
test_fldigi_logbook_values_with_markup_characters_round_trip() {
  fd_env; fd_logbook
  local u1='https://a.example/?x=1&y=<2>' k1='a "b" & <c> d' u2='https://b.example/?q=&r=<"x y">' k2='k & "2" <z> w'
  FD_LOGBOOK=$(jq -cn --arg u "$u1" --arg k "$k1" '{url: $u, key: $k, station_id: "1"}')
  app_fldigi_firstfill radio2
  local f; f=$(app_fldigi_settings_file radio2)
  assert_eq "$(dxb_xml_get "$f" CLOUDLOG_API_URL)" "$u1"
  assert_eq "$(dxb_xml_get "$f" CLOUDLOG_API_KEY)" "$k1"
  FD_LOGBOOK=$(jq -cn --arg u "$u2" --arg k "$k2" '{url: $u, key: $k, station_id: "1"}')
  assert_eq "$(app_fldigi_logbook_follow radio2)" ""
  assert_eq "$(dxb_xml_get "$f" CLOUDLOG_API_URL)" "$u2"
  assert_eq "$(dxb_xml_get "$f" CLOUDLOG_API_KEY)" "$k2"
  fd_unlogbook
}
