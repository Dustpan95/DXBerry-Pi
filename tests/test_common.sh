#!/usr/bin/env bash
# shellcheck disable=SC1091,SC2034,SC2016
source "$DXB_LIB/common.sh"

test_set_kv_replaces_first_match_keeps_rest_and_appends() {
  printf '# c\nA=1\n  A=2\nB=x\n' > "$TEST_TMP/f"
  chmod 600 "$TEST_TMP/f"
  dxb_set_kv "$TEST_TMP/f" A new
  assert_eq "$(cat "$TEST_TMP/f")" $'# c\nA=new\n  A=2\nB=x'
  assert_eq "$(stat -c %a "$TEST_TMP/f")" "600"
  dxb_set_kv "$TEST_TMP/f" C 'v=with=equals and $dollar \backslash'
  assert_file_contains "$TEST_TMP/f" 'C=v=with=equals and $dollar \backslash'
  assert_eq "$(stat -c %a "$TEST_TMP/f")" "600"
  # A failed replace must leave the original untouched and return non-zero.
  printf 'KEY=old\n' > "$TEST_TMP/fail.conf"
  mkdir "$TEST_TMP/fail.conf.dxbtmp"
  assert_fails dxb_set_kv "$TEST_TMP/fail.conf" KEY new 2> /dev/null
  assert_eq "$(< "$TEST_TMP/fail.conf")" "KEY=old"
}

test_set_kv_handles_bracketed_keys_and_missing_file() {
  printf "aWIFI_SSID[0]=''\naWIFI_SSID0=keep\n" > "$TEST_TMP/w"
  dxb_set_kv "$TEST_TMP/w" 'aWIFI_SSID[0]' "'Home'"
  assert_eq "$(cat "$TEST_TMP/w")" $'aWIFI_SSID[0]=\'Home\'\naWIFI_SSID0=keep'
  dxb_set_kv "$TEST_TMP/new" K V
  assert_eq "$(cat "$TEST_TMP/new")" "K=V"
}

test_render_substitutes_and_rejects_unresolved() {
  printf 'iface @IFACE@ inet @METHOD@\n@ADDRESS@\n' > "$TEST_TMP/t.tmpl"
  assert_eq "$(dxb_render "$TEST_TMP/t.tmpl" IFACE=eth0 METHOD=static 'ADDRESS=address 10.0.0.5/24')" $'iface eth0 inet static\naddress 10.0.0.5/24'
  assert_fails dxb_render "$TEST_TMP/t.tmpl" IFACE=eth0
  printf 'eth @ETH0_MAC@\n' > "$TEST_TMP/t2.tmpl"
  assert_fails dxb_render "$TEST_TMP/t2.tmpl" IFACE=eth0
  assert_fails dxb_render "$TEST_TMP/does-not-exist.tmpl" 2> /dev/null
}

test_write_if_changed_reports_change() {
  assert_ok dxb_write_if_changed "$TEST_TMP/o" "hello" 600
  assert_fails dxb_write_if_changed "$TEST_TMP/o" "hello"
  assert_ok dxb_write_if_changed "$TEST_TMP/o" "hello2"
  assert_eq "$(stat -c %a "$TEST_TMP/o")" "600"
  [[ -f $TEST_TMP/o.dxbtmp ]] && _fail "leftover temp file $TEST_TMP/o.dxbtmp"
  ln -s /nonexistent "$TEST_TMP/link"
  assert_ok dxb_write_if_changed "$TEST_TMP/link" "x"
  [[ -L $TEST_TMP/link ]] && _fail "symlink should have been replaced by a file"
  [[ -f $TEST_TMP/link.dxbtmp ]] && _fail "leftover temp file $TEST_TMP/link.dxbtmp"
}

test_status_file_lists_failures() {
  DXB_STATUS_LINES=(); DXB_FAILED_STEPS=()
  DXB_LOG_FILE=$TEST_TMP/log
  dxb_status_add "hostname: x"
  dxb_step_failed graywolf "download failed"
  dxb_status_write "$TEST_TMP/status.txt"
  assert_file_contains "$TEST_TMP/status.txt" "hostname: x"
  assert_file_contains "$TEST_TMP/status.txt" "FAILED STEPS:"
  assert_file_contains "$TEST_TMP/status.txt" "graywolf: download failed"
  assert_file_contains "$TEST_TMP/log" "[ERROR] graywolf: download failed"
}

test_boot_dir_override_and_squote() {
  DXB_BOOT_DIR=$TEST_TMP/bootfs
  assert_eq "$(dxb_boot_dir)" "$TEST_TMP/bootfs"
  unset DXB_BOOT_DIR
  assert_eq "$(dxb_squote "it's")" "'it'\\''s'"
}
