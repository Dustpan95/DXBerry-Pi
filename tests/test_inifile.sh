#!/usr/bin/env bash
# shellcheck disable=SC1091,SC2034,SC2016,SC1003
source "$DXB_LIB/common.sh"
source "$DXB_LIB/inifile.sh"

test_ini_set_changes_one_key_and_keeps_everything_else() {
  cp "$DXB_ROOT/tests/fixtures/apps/wsjtx.ini" "$TEST_TMP/w.ini"
  assert_ok dxb_ini_set "$TEST_TMP/w.ini" Configuration MyCall W0BTE
  assert_eq "$(diff "$DXB_ROOT/tests/fixtures/apps/wsjtx.ini" "$TEST_TMP/w.ini" | grep -c '^[<>]')" "2"
  assert_eq "$(dxb_ini_get "$TEST_TMP/w.ini" Configuration MyCall)" "W0BTE"
  dxb_ini_set "$TEST_TMP/w.ini" Configuration MyCall W0BTE; assert_eq "$?" "1"
  assert_eq "$(dxb_ini_get "$TEST_TMP/w.ini" Common DialFreq)" "14074000"   # same key name, other section untouched
}
test_ini_set_appends_missing_key_and_section() {
  cp "$DXB_ROOT/tests/fixtures/apps/wsjtx.ini" "$TEST_TMP/w.ini"
  dxb_ini_set "$TEST_TMP/w.ini" Configuration SoundInName "$(dxb_ini_qstring 'plughw:CARD=RADIO2,DEV=0')"
  assert_eq "$(dxb_ini_get "$TEST_TMP/w.ini" Configuration SoundInName)" '"plughw:CARD=RADIO2,DEV=0"'
  assert_eq "$(awk '/^\[/{s=$0} /^SoundInName=/{print s}' "$TEST_TMP/w.ini")" "[Configuration]"
  dxb_ini_set "$TEST_TMP/new.ini" Configuration Rig 'Hamlib NET rigctl'
  assert_eq "$(cat "$TEST_TMP/new.ini")" $'[Configuration]\nRig=Hamlib NET rigctl'
}
test_ini_qenum_matches_what_qt_wrote() {
  assert_eq "$(dxb_ini_qenum TransceiverFactory::PTTMethod PTT_method_VOX)" \
    "$(dxb_ini_get "$DXB_ROOT/tests/fixtures/apps/wsjtx.ini" Configuration PTTMethod)"
  assert_eq "$(dxb_ini_qenum TransceiverFactory::PTTMethod PTT_method_CAT)" '@Variant(\0\0\0\x7f\0\0\0\x1eTransceiverFactory::PTTMethod\0\0\0\0\xfPTT_method_CAT\0)'
}
test_ini_qenum_round_trips_every_enum_in_the_fixture() {
  local f="$DXB_ROOT/tests/fixtures/apps/wsjtx.ini" row key type val
  for row in \
    'PTTMethod TransceiverFactory::PTTMethod PTT_method_VOX' \
    'SplitMode TransceiverFactory::SplitMode split_mode_none' \
    'TXAudioSource TransceiverFactory::TXAudioSource TX_audio_source_front' \
    'CATDataBits TransceiverFactory::DataBits default_data_bits' \
    'CATStopBits TransceiverFactory::StopBits default_stop_bits' \
    'CATHandshake TransceiverFactory::Handshake handshake_default'; do
    read -r key type val <<< "$row"
    assert_eq "$(dxb_ini_qenum "$type" "$val")" "$(dxb_ini_get "$f" Configuration "$key")"
  done
}
test_ini_qstring_quotes_only_when_qt_does() {
  assert_eq "$(dxb_ini_qstring 'Hamlib NET rigctl')" 'Hamlib NET rigctl'
  assert_eq "$(dxb_ini_qstring 'a;b')" '"a;b"'
  assert_eq "$(dxb_ini_qstring ' lead')" '" lead"'
  assert_eq "$(dxb_ini_qstring 'say "hi", ok')" '"say \"hi\", ok"'
  assert_eq "$(dxb_ini_qstring 'a\b=c')" '"a\\b=c"'
}
test_ini_set_keeps_mode_and_creates_0644() {
  printf '[A]\nk=1\n' > "$TEST_TMP/m.ini"; chmod 600 "$TEST_TMP/m.ini"
  dxb_ini_set "$TEST_TMP/m.ini" A k 2
  assert_eq "$(stat -c %a "$TEST_TMP/m.ini")" "600"
  dxb_ini_set "$TEST_TMP/n.ini" A k 2
  assert_eq "$(stat -c %a "$TEST_TMP/n.ini")" "644"
  dxb_ini_set "$TEST_TMP/no/such/dir/x.ini" A k 2; assert_eq "$?" "6"
}
test_ini_values_with_awk_specials_go_in_verbatim() {
  printf '[A]\nk=1\n' > "$TEST_TMP/s.ini"
  dxb_ini_set "$TEST_TMP/s.ini" A k 'a\nb&c"d\\'
  assert_eq "$(dxb_ini_get "$TEST_TMP/s.ini" A k)" 'a\nb&c"d\\'
}
test_xml_set_replaces_one_tag() {
  cp "$DXB_ROOT/tests/fixtures/apps/fldigi_def.xml" "$TEST_TMP/f.xml"
  assert_ok dxb_xml_set "$TEST_TMP/f.xml" HAMRIGDEVICE 127.0.0.1:4534
  assert_eq "$(dxb_xml_get "$TEST_TMP/f.xml" HAMRIGDEVICE)" "127.0.0.1:4534"
  assert_eq "$(diff "$DXB_ROOT/tests/fixtures/apps/fldigi_def.xml" "$TEST_TMP/f.xml" | grep -c '^[<>]')" "2"
  dxb_xml_set "$TEST_TMP/f.xml" MYQTH 'A & B <x>'
  assert_file_contains "$TEST_TMP/f.xml" '<MYQTH>A &amp; B &lt;x&gt;</MYQTH>'
  assert_eq "$(dxb_xml_get "$TEST_TMP/f.xml" MYQTH)" 'A & B <x>'
}
test_xml_set_inserts_missing_tag_and_creates_file() {
  cp "$DXB_ROOT/tests/fixtures/apps/fldigi_def.xml" "$TEST_TMP/f.xml"
  dxb_xml_set "$TEST_TMP/f.xml" NEWTAG_X 1
  assert_eq "$(tail -2 "$TEST_TMP/f.xml")" $'<NEWTAG_X>1</NEWTAG_X>\n</FLDIGI_DEFS>'
  dxb_xml_set "$TEST_TMP/f.xml" NEWTAG_X 1; assert_eq "$?" "1"
  dxb_xml_set "$TEST_TMP/new.xml" T v
  assert_eq "$(cat "$TEST_TMP/new.xml")" $'<?xml version="1.0"?>\n<FLDIGI_DEFS>\n<T>v</T>\n</FLDIGI_DEFS>'
  assert_eq "$(stat -c %a "$TEST_TMP/new.xml")" "644"
}
test_prefs_set_flrig_port() {
  cp "$DXB_ROOT/tests/fixtures/apps/flrig.prefs" "$TEST_TMP/p"
  assert_ok dxb_prefs_set "$TEST_TMP/p" xml_port 12346
  assert_eq "$(dxb_prefs_get "$TEST_TMP/p" xml_port)" "12346"
  assert_eq "$(dxb_prefs_get "$TEST_TMP/p" xcvr_name)" "NONE"
}
test_prefs_set_appends_missing_key_in_dot_section() {
  cp "$DXB_ROOT/tests/fixtures/apps/flrig.prefs" "$TEST_TMP/p"
  dxb_prefs_set "$TEST_TMP/p" brand_new_key 7
  assert_eq "$(dxb_prefs_get "$TEST_TMP/p" brand_new_key)" "7"
  assert_eq "$(diff "$DXB_ROOT/tests/fixtures/apps/flrig.prefs" "$TEST_TMP/p" | grep -c '^[<>]')" "1"
}
