#!/usr/bin/env bash
# shellcheck disable=SC1091,SC2034
source "$DXB_LIB/common.sh"
source "$DXB_LIB/config.sh"
source "$DXB_LIB/radio.sh"
source "$DXB_ROOT/tests/fixtures/sysfs.sh"

radio_env() {
  export DXB_STATE_DIR=$TEST_TMP/state DXB_LOG_FILE=$TEST_TMP/state/log DXB_SYSFS_ROOT=$TEST_TMP/sys \
    DXB_RADIO_PROFILES=$DXB_ROOT/provision/share/radio-profiles.tsv DXB_RADIOS_FILE=$TEST_TMP/state/radios.json
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

test_record_roundtrip_and_validation() {
  radio_env
  assert_ok dxb_radio_load
  assert_eq "$(jq -c '.radios' <<< "$DXB_RADIOS")" "{}"
  assert_ok dxb_radio_save "$DXB_RADIOS"
  assert_eq "$(stat -c %a "$DXB_RADIOS_FILE")" "600"
  echo 'not json' > "$DXB_RADIOS_FILE"
  dxb_radio_load; assert_eq "$?" "6"
  radio_env
  local bad; bad=$(jq -c '.radios.radio1 = {label:"x",audio:null,cat:null,hid:null,ptt:{method:"laser",gpio_line:null},rig:{model:1,baud:0,ptt_type:"NONE"},rigctld_port:4532,wiring:"full",owner:""}' <<< "$(dxb_radio_empty_record)")
  dxb_radio_validate "$bad"; assert_eq "$?" "2"
}

test_add_digirig_pins_functions_and_profile_defaults() {
  radio_env; fx_scene "$DXB_SYSFS_ROOT" digirig; dxb_radio_scan_cache; dxb_radio_load
  assert_ok dxb_radio_add radio1 '{"audio":"1","cat":"2","label":"TM-V71"}'
  local r; r=$(dxb_radio_get radio1)
  assert_eq "$(jq -r '.audio.path' <<< "$r")" "usb-0:1.3:1.0"
  assert_eq "$(jq -r '.cat.path' <<< "$r")" "usb-0:1.4:1.0"
  assert_eq "$(jq -r '.cat.serial' <<< "$r")" "0001"
  assert_eq "$(jq -r '.hid.path' <<< "$r")" "usb-0:1.3:1.3"
  assert_eq "$(jq -r '.profile' <<< "$r")" "0d8c:013c"
  assert_eq "$(jq -r '.ptt.method + " " + .rig.ptt_type + " " + (.rig.baud|tostring)' <<< "$r")" "rigctld RTS 57600"
  assert_eq "$(jq -r '.rigctld_port' <<< "$r")" "4532"
  assert_eq "$(jq -r '.wiring + "/" + .owner' <<< "$r")" "full/"
  assert_eq "$(jq -r '.radios.radio1.label' "$DXB_RADIOS_FILE")" "TM-V71"
}

test_add_ic705_second_serial_and_overrides() {
  radio_env; fx_scene "$DXB_SYSFS_ROOT" ic705; dxb_radio_scan_cache; dxb_radio_load
  assert_ok dxb_radio_add hf '{"audio":"1","cat":"1:2","model":3073,"baud":19200,"wiring":"names"}'
  local r; r=$(dxb_radio_get hf)
  assert_eq "$(jq -r '.cat.path' <<< "$r")" "usb-0:1.2:1.4"
  assert_eq "$(jq -r '.rig.baud' <<< "$r")" "19200"
  assert_eq "$(jq -r '.rig.ptt_type' <<< "$r")" "RIG"
  assert_eq "$(jq -r '.hid' <<< "$r")" "null"
}

test_add_rejects_bad_name_duplicate_and_missing_function() {
  radio_env; fx_scene "$DXB_SYSFS_ROOT" split; dxb_radio_scan_cache; dxb_radio_load
  dxb_radio_add Radio1 '{"audio":"1"}'; assert_eq "$?" "2"
  dxb_radio_add r9 '{"audio":"2"}'; assert_eq "$?" "2"          # candidate 2 has no audio function
  dxb_radio_add r9 '{"audio":"7"}'; assert_eq "$?" "2"          # no such candidate
  assert_ok dxb_radio_add r9 '{"audio":"1","cat":"2"}'
  dxb_radio_add r9 '{"audio":"1"}'; assert_eq "$?" "2"          # duplicate
}

test_add_rejects_multiline_or_overlong_label() {
  radio_env; fx_scene "$DXB_SYSFS_ROOT" digirig; dxb_radio_scan_cache; dxb_radio_load
  dxb_radio_add x '{"audio":"1","label":"bad\nlabel"}'; assert_eq "$?" "2"   # \n is a real newline once JSON-parsed
  dxb_radio_add x "{\"audio\":\"1\",\"label\":\"$(printf 'a%.0s' $(seq 1 41))\"}"; assert_eq "$?" "2"   # 41 chars
  assert_ok dxb_radio_add x "{\"audio\":\"1\",\"label\":\"$(printf 'a%.0s' $(seq 1 40))\"}"            # 40 chars, accepted
}

test_ports_allocate_lowest_free_even() {
  radio_env; fx_scene "$DXB_SYSFS_ROOT" two-digirigs; dxb_radio_scan_cache; dxb_radio_load
  assert_ok dxb_radio_add a '{"audio":"3","cat":"4"}'
  assert_ok dxb_radio_add b '{"audio":"1","cat":"2"}'
  assert_eq "$(jq -r '.rigctld_port' <<< "$(dxb_radio_get b)")" "4534"
  assert_ok dxb_radio_remove a
  assert_ok dxb_radio_add c '{"audio":"3","cat":"4"}'
  assert_eq "$(jq -r '.rigctld_port' <<< "$(dxb_radio_get c)")" "4532"
  dxb_radio_get a > /dev/null; assert_eq "$?" "3"
}

test_set_changes_fields_keeps_port_and_owner() {
  radio_env; fx_scene "$DXB_SYSFS_ROOT" digirig; dxb_radio_scan_cache; dxb_radio_load
  assert_ok dxb_radio_add radio1 '{"audio":"1","cat":"2"}'
  assert_ok _dxb_radio_set_owner radio1 graywolf
  assert_ok dxb_radio_set radio1 '{"ptt":"cm108","label":"HT","cat":"none"}'
  local r; r=$(dxb_radio_get radio1)
  assert_eq "$(jq -r '.ptt.method + " " + .label + " " + (.cat|tostring) + " " + .owner + " " + (.rigctld_port|tostring) + " " + .rig.ptt_type' <<< "$r")" "cm108 HT null graywolf 4532 NONE"
  dxb_radio_set radio1 '{"wiring":"sideways"}'; assert_eq "$?" "2"
  dxb_radio_set nope '{"label":"x"}'; assert_eq "$?" "3"
}

test_pin_selector_rejects_zero_and_out_of_range_k() {
  radio_env; fx_scene "$DXB_SYSFS_ROOT" ic705; dxb_radio_scan_cache; dxb_radio_load
  dxb_radio_add x '{"cat":"1:0"}'; assert_eq "$?" "2"          # K < 1
  dxb_radio_add x '{"cat":"1:3"}'; assert_eq "$?" "2"          # only two serial functions
}

test_add_explicit_audio_none_falls_back_to_cat_profile() {
  radio_env; fx_scene "$DXB_SYSFS_ROOT" split; dxb_radio_scan_cache; dxb_radio_load
  assert_ok dxb_radio_add c '{"audio":"none","cat":"2"}'
  local r; r=$(dxb_radio_get c)
  assert_eq "$(jq -r '.profile' <<< "$r")" "0403:6001"
  assert_eq "$(jq -r '.rig.ptt_type' <<< "$r")" "RIG"
}

test_add_drops_serial_ptt_type_without_a_serial_pin() {
  radio_env; fx_scene "$DXB_SYSFS_ROOT" digirig; dxb_radio_scan_cache; dxb_radio_load
  assert_ok dxb_radio_add d '{"audio":"1"}'
  assert_eq "$(jq -r '.rig.ptt_type' <<< "$(dxb_radio_get d)")" "NONE"
  assert_ok dxb_radio_add e '{"audio":"1","ptt_serial":"2","ptt_type":"RTS"}'
  assert_eq "$(jq -r '.rig.ptt_type' <<< "$(dxb_radio_get e)")" "RTS"
}
