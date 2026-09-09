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
  [[ -n $(find "$TEST_TMP" -maxdepth 1 -name 'o.dxbtmp*') ]] && _fail "leftover temp file for $TEST_TMP/o"
  # the temp name carries the writer's pid, so a stale or foreign "$dest.dxbtmp" cannot block a write
  mkdir "$TEST_TMP/o.dxbtmp"
  assert_ok dxb_write_if_changed "$TEST_TMP/o" "hello3"
  assert_eq "$(< "$TEST_TMP/o")" "hello3"
  rmdir "$TEST_TMP/o.dxbtmp"
  ln -s /nonexistent "$TEST_TMP/link"
  assert_ok dxb_write_if_changed "$TEST_TMP/link" "x"
  [[ -L $TEST_TMP/link ]] && _fail "symlink should have been replaced by a file"
  [[ -n $(find "$TEST_TMP" -maxdepth 1 -name 'link.dxbtmp*') ]] && _fail "leftover temp file for $TEST_TMP/link"
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

test_status_tail_names_first_boot_flag_only_in_first_boot_mode() {
  DXB_STATUS_LINES=(); DXB_FAILED_STEPS=()
  DXB_LOG_FILE=$TEST_TMP/log
  dxb_step_failed network "simulated failure"
  DXB_MODE=first-boot
  dxb_status_write "$TEST_TMP/first-boot-status.txt"
  assert_file_contains "$TEST_TMP/first-boot-status.txt" "Fix the cause, then run: sudo dxberry-provision --first-boot"
  DXB_MODE=run
  dxb_status_write "$TEST_TMP/run-status.txt"
  assert_file_contains "$TEST_TMP/run-status.txt" "Fix the cause, then run: sudo dxberry-provision"
  assert_file_not_contains "$TEST_TMP/run-status.txt" "sudo dxberry-provision --first-boot"
  unset DXB_MODE
}

test_boot_dir_override_and_squote() {
  DXB_BOOT_DIR=$TEST_TMP/bootfs
  assert_eq "$(dxb_boot_dir)" "$TEST_TMP/bootfs"
  unset DXB_BOOT_DIR
  assert_eq "$(dxb_squote "it's")" "'it'\\''s'"
}

test_ensure_line_appends_once() {
  local f=$TEST_TMP/config.txt
  printf 'arm_64bit=1\n' > "$f"
  assert_ok dxb_ensure_line "$f" 'enable_uart=1'
  assert_fails dxb_ensure_line "$f" 'enable_uart=1'
  assert_eq "$(grep -c '^enable_uart=1$' "$f")" "1"
  assert_eq "$(head -1 "$f")" "arm_64bit=1"
}

test_cfgtxt_ensure_line_appends_under_an_all_section() {
  local f=$TEST_TMP/config.txt
  printf 'arm_64bit=1\n[cm4]\ndtoverlay=dwc2,dr_mode=host\n' > "$f"
  assert_ok dxb_cfgtxt_ensure_line "$f" 'enable_uart=1'
  assert_eq "$(tail -2 "$f")" $'[all]\nenable_uart=1'
  assert_ok dxb_cfgtxt_ensure_line "$f" 'dtoverlay=disable-bt'   # the file already ends in [all]
  assert_eq "$(grep -c '^\[all\]$' "$f")" "1"
  assert_eq "$(tail -1 "$f")" "dtoverlay=disable-bt"
  assert_fails dxb_cfgtxt_ensure_line "$f" 'enable_uart=1'       # already present: untouched
  assert_eq "$(grep -c '^enable_uart=1$' "$f")" "1"
}

test_cfgtxt_ensure_line_needs_no_header_without_sections() {
  local f=$TEST_TMP/plain-config.txt
  printf 'arm_64bit=1\n' > "$f"
  assert_ok dxb_cfgtxt_ensure_line "$f" 'enable_uart=1'
  assert_eq "$(cat "$f")" $'arm_64bit=1\nenable_uart=1'
}
