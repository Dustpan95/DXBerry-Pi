#!/usr/bin/env bash
# shellcheck disable=SC1091,SC2034
source "$DXB_LIB/common.sh"
source "$DXB_LIB/config.sh"
source "$DXB_LIB/radio.sh"
source "$DXB_ROOT/tests/fixtures/sysfs.sh"

radio_env() {
  export DXB_STATE_DIR=$TEST_TMP/state DXB_LOG_FILE=$TEST_TMP/state/log DXB_SYSFS_ROOT=$TEST_TMP/sys \
    DXB_RADIO_PROFILES=$DXB_ROOT/provision/share/radio-profiles.tsv
  mkdir -p "$DXB_STATE_DIR"
  DXB_STATUS_LINES=(); DXB_FAILED_STEPS=()
}

test_profiles_tsv_parses() {
  radio_env
  local j; j=$(dxb_radio_profiles_json)
  assert_eq "$(jq -r '.[] | select(.vidpid=="0d8c:013c") | .name' <<< "$j")" "DigiRig Mobile"
  assert_eq "$(jq -r '.[] | select(.vidpid=="0d8c:013c") | .ptt_type' <<< "$j")" "RTS"
  assert_eq "$(jq -r '.[] | select(.vidpid=="0c26:0036") | .model' <<< "$j")" "3085"
}

test_scan_digirig_two_candidates() {
  radio_env; fx_scene "$DXB_SYSFS_ROOT" digirig
  local j; j=$(dxb_radio_scan)
  assert_eq "$(jq 'length' <<< "$j")" "2"
  assert_eq "$(jq -r '.[0].port' <<< "$j")" "usb-0:1.3"
  assert_eq "$(jq -r '.[0].name' <<< "$j")" "DigiRig Mobile"
  assert_eq "$(jq -r '.[0].functions | map(.kind) | join(",")' <<< "$j")" "audio,hid"
  assert_eq "$(jq -r '.[0].functions[0].path' <<< "$j")" "usb-0:1.3:1.0"
  assert_eq "$(jq -r '.[0].functions[0].kernel' <<< "$j")" "card1"
  assert_eq "$(jq -r '.[1].functions[0].kind + " " + .[1].functions[0].serial' <<< "$j")" "serial 0001"
  assert_eq "$(jq -r '.[1].profile' <<< "$j")" "10c4:ea60"
  assert_eq "$(jq -r '.[1].name' <<< "$j")" "CP2102 serial"
  assert_eq "$(jq -r '.[1].defaults.ptt' <<< "$j")" "rigctld"
}

test_scan_ic705_one_candidate_two_serials() {
  radio_env; fx_scene "$DXB_SYSFS_ROOT" ic705
  local j; j=$(dxb_radio_scan)
  assert_eq "$(jq 'length' <<< "$j")" "1"
  assert_eq "$(jq -r '.[0].functions | map(.kind) | join(",")' <<< "$j")" "audio,serial,serial"
  assert_eq "$(jq -r '.[0].functions[1].path' <<< "$j")" "usb-0:1.2:1.2"
  assert_eq "$(jq -r '.[0].defaults.model' <<< "$j")" "3085"
  assert_eq "$(jq -r '.[0].functions[0].serial' <<< "$j")" "IC-705_12345678"
}

test_scan_ignores_onboard_audio_and_orphan_hid() {
  radio_env; fx_scene "$DXB_SYSFS_ROOT" none
  # a USB keyboard-like HID with no sound function must not appear
  fx_usb_device "$DXB_SYSFS_ROOT" 1-1.1 046d c31c "" "Keyboard"
  fx_usb_function "$DXB_SYSFS_ROOT" 1-1.1 0 hid hidraw0
  assert_eq "$(dxb_radio_scan)" "[]"
}

test_scan_two_digirigs_distinct_ports() {
  radio_env; fx_scene "$DXB_SYSFS_ROOT" two-digirigs
  local j; j=$(dxb_radio_scan)
  assert_eq "$(jq -r 'map(.port) | join(" ")' <<< "$j")" "usb-0:1.1 usb-0:1.2 usb-0:1.3 usb-0:1.4"
  assert_eq "$(jq -r 'map(.index) | join(" ")' <<< "$j")" "1 2 3 4"
}
