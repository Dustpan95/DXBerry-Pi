#!/usr/bin/env bash
# shellcheck disable=SC1091,SC2034,SC2317
source "$DXB_LIB/common.sh"
source "$DXB_LIB/config.sh"
source "$DXB_LIB/radio.sh"
source "$DXB_LIB/radio_udev.sh"
source "$DXB_ROOT/tests/fixtures/sysfs.sh"

udev_env() {
  export DXB_STATE_DIR=$TEST_TMP/state DXB_LOG_FILE=$TEST_TMP/state/log DXB_SYSFS_ROOT=$TEST_TMP/sys \
    DXB_RADIO_PROFILES=$DXB_ROOT/provision/share/radio-profiles.tsv DXB_RADIOS_FILE=$TEST_TMP/state/radios.json \
    DXB_UDEV_RULES_FILE=$TEST_TMP/etc/70.rules DXB_MODPROBE_FILE=$TEST_TMP/etc/dxberry-audio.conf DXB_UDEVADM=fake_udevadm
  mkdir -p "$DXB_STATE_DIR" "$TEST_TMP/etc"; : > "$TEST_TMP/calls"
  fake_udevadm() { echo "udevadm $*" >> "$TEST_TMP/calls"; }
  DXB_STATUS_LINES=(); DXB_FAILED_STEPS=()
}
digirig_record() {
  fx_scene "$DXB_SYSFS_ROOT" digirig; dxb_radio_scan_cache; dxb_radio_load
  dxb_radio_add radio1 '{"audio":"1","cat":"2","label":"TM-V71"}' > /dev/null
}

test_udev_rules_match_expected_file() {
  udev_env; digirig_record
  dxb_radio_udev_rules "$DXB_RADIOS" > "$TEST_TMP/out.rules"
  cmp -s "$TEST_TMP/out.rules" "$DXB_ROOT/tests/fixtures/udev/digirig.rules" || { _fail "rules differ"; diff "$DXB_ROOT/tests/fixtures/udev/digirig.rules" "$TEST_TMP/out.rules" >&2; }
}

test_udev_rules_empty_record_has_head_and_trailer_only() {
  udev_env; dxb_radio_load
  local out; out=$(dxb_radio_udev_rules "$DXB_RADIOS")
  assert_contains "$out" 'IMPORT{builtin}="path_id"'
  assert_contains "$out" 'TAG=="dxberry-radio", ACTION=="add|remove"'
  assert_not_contains "$out" 'SYMLINK'
}

test_udev_rules_ptt_serial_and_no_audio() {
  udev_env; fx_scene "$DXB_SYSFS_ROOT" two-digirigs; dxb_radio_scan_cache; dxb_radio_load
  dxb_radio_add cat1 '{"cat":"2","ptt_serial":"4","ptt_type":"RTS"}' > /dev/null
  local out; out=$(dxb_radio_udev_rules "$DXB_RADIOS")
  assert_contains "$out" 'SYMLINK+="dxberry/cat1-cat"'
  assert_contains "$out" 'ENV{ID_PATH}=="*-usb-0:1.4:1.0", SYMLINK+="dxberry/cat1-ptt"'
  assert_not_contains "$out" 'ATTR{id}'
}

test_udev_write_reloads_only_on_change() {
  udev_env; digirig_record
  assert_ok dxb_radio_udev_write "$DXB_RADIOS"
  assert_contains "$(cat "$TEST_TMP/calls")" "udevadm control --reload"
  assert_contains "$(cat "$TEST_TMP/calls")" "udevadm trigger --action=add --subsystem-match=sound --subsystem-match=tty --subsystem-match=hidraw"
  : > "$TEST_TMP/calls"
  dxb_radio_udev_write "$DXB_RADIOS"; assert_eq "$?" "1"
  assert_eq "$(cat "$TEST_TMP/calls")" ""
}

test_modprobe_install_idempotent() {
  udev_env
  assert_ok dxb_radio_modprobe_install
  assert_file_contains "$DXB_MODPROBE_FILE" "options snd slots=snd_usb_audio"
  dxb_radio_modprobe_install; assert_eq "$?" "1"
}
