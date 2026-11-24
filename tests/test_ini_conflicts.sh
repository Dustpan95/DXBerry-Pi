#!/usr/bin/env bash
# shellcheck disable=SC1091,SC2034
source "$DXB_LIB/common.sh"
source "$DXB_LIB/inifile.sh"

ic_refuse() {
  local content=$1 sec=${2:-Configuration} key=${3:-UDPServer} rc out
  printf '%s' "$content" > "$TEST_TMP/ambiguous.ini"
  chmod 640 "$TEST_TMP/ambiguous.ini"
  cp "$TEST_TMP/ambiguous.ini" "$TEST_TMP/before"
  dxb_ini_set "$TEST_TMP/ambiguous.ini" "$sec" "$key" new-destination > "$TEST_TMP/out" 2> "$TEST_TMP/err"; rc=$?
  assert_eq "$rc" 6
  cmp -s "$TEST_TMP/ambiguous.ini" "$TEST_TMP/before" || _fail "conflicting INI was changed"
  assert_eq "$(stat -c %a "$TEST_TMP/ambiguous.ini")" 640
  out=$(dxb_ini_get "$TEST_TMP/ambiguous.ini" "$sec" "$key" 2> /dev/null); rc=$?
  assert_eq "$rc" 6
  assert_eq "$out" ''
  assert_not_contains "$(cat "$TEST_TMP/err")" private-destination
}

test_ini_duplicate_keys_cannot_report_a_false_success() {
  ic_refuse $'[Configuration]\nUDPServer=first\nUDPServer=private-destination\n'
}

test_ini_duplicate_target_sections_are_refused() {
  ic_refuse $'[Configuration]\nUDPServer=first\n[Other]\nx=1\n[Configuration]\nUDPServer=private-destination\n'
}

test_ini_percent_encoded_key_aliases_are_refused() {
  ic_refuse $'[Configuration]\nUDPServer=first\nUDP%53erver=private-destination\n'
}

test_ini_unicode_encoded_key_aliases_are_refused() {
  ic_refuse $'[Configuration]\nUDPServer=first\nUDP%U0053erver=private-destination\n'
}

test_ini_encoded_section_alias_is_not_mistaken_for_missing() {
  ic_refuse $'[Con%66iguration]\nUDPServer=private-destination\n'
}

test_ini_root_group_key_aliases_are_refused() {
  ic_refuse $'[General]\nConfiguration\\UDPServer=private-destination\n[Configuration]\nUDPServer=first\n'
}

test_ini_whitespace_aliases_are_not_reported_as_absent() {
  ic_refuse $'[Configuration]\n UDPServer =private-destination\n'
}

test_ini_continued_values_cannot_hide_target_keys() {
  ic_refuse $'[Configuration]\nOther=one\\\nUDPServer=private-destination\n'
}

test_ini_crlf_and_bom_layouts_are_refused_without_conversion() {
  ic_refuse $'[Configuration]\r\nUDPServer=private-destination\r\n'
  ic_refuse $'\xef\xbb\xbf[Configuration]\nUDPServer=private-destination\n'
}

test_ini_malformed_headers_and_encoded_names_are_refused() {
  ic_refuse $'[Configuration]\nUDPServer=first\n[unfinished\nUDPServer=private-destination\n'
  ic_refuse $'[Configuration]\nUDPServer=first\nBad%Q0=private-destination\n'
}

test_ini_commented_keys_and_unrelated_qt_groups_remain_usable() {
  local f=$TEST_TMP/good.ini rc
  cat > "$f" <<'INI'
[General]
Configuration\DifferentKey=keep
[Configuration]
;UDPServer=ignored
#UDPServer=ignored-too
UDPServer=first
Other=@Variant(\0\0%U0053)
WindowsPath=ends-in-a-pair\\
[WSPR%20Band%20Hopping]
x=unchanged
[MultiSettings]
Other%20profile\Configuration\UDPServer=private-destination
INI
  cp "$f" "$TEST_TMP/before"
  dxb_ini_set "$f" Configuration UDPServer new-destination; rc=$?
  assert_eq "$rc" 0
  assert_eq "$(dxb_ini_get "$f" Configuration UDPServer)" new-destination
  assert_file_contains "$f" 'Other%20profile\Configuration\UDPServer=private-destination'
  assert_eq "$(diff "$TEST_TMP/before" "$f" | grep -c '^[<>]')" 2
}

test_ini_identical_names_in_other_sections_do_not_conflict() {
  local f=$TEST_TMP/other.ini
  printf '[A]\nk=first\n[B]\nk=second\n' > "$f"
  assert_ok dxb_ini_set "$f" A k changed
  assert_eq "$(dxb_ini_get "$f" A k)" changed
  assert_eq "$(dxb_ini_get "$f" B k)" second
}

test_ini_general_root_and_literal_general_group_stay_distinct() {
  local f=$TEST_TMP/general.ini
  printf '[General]\nk=first\n[%%General]\nk=literal\n' > "$f"
  assert_ok dxb_ini_set "$f" General k changed
  assert_eq "$(dxb_ini_get "$f" General k)" changed
  assert_file_contains "$f" 'k=literal'
}
