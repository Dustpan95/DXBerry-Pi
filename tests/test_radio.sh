#!/usr/bin/env bash
# shellcheck disable=SC1091,SC2034,SC2317
source "$DXB_LIB/common.sh"
source "$DXB_LIB/config.sh"
source "$DXB_LIB/radio.sh"
source "$DXB_LIB/radio_udev.sh"
source "$DXB_LIB/rigctld.sh"
source "$DXB_ROOT/tests/fixtures/sysfs.sh"

radio_env() {
  export DXB_STATE_DIR=$TEST_TMP/state DXB_LOG_FILE=$TEST_TMP/state/log DXB_SYSFS_ROOT=$TEST_TMP/sys \
    DXB_RADIO_PROFILES=$DXB_ROOT/provision/share/radio-profiles.tsv DXB_RADIOS_FILE=$TEST_TMP/state/radios.json \
    DXB_UDEV_RULES_FILE=$TEST_TMP/etc/70.rules DXB_MODPROBE_FILE=$TEST_TMP/etc/dxberry-audio.conf DXB_UDEVADM=fake_udevadm \
    DXB_RIGCTLD_RUN_DIR=$TEST_TMP/run/rigctld DXB_SYSTEMD_DIR=$TEST_TMP/systemd DXB_TMPFILES_DIR=$TEST_TMP/tmpfiles \
    DXB_RADIOS_STATE=$TEST_TMP/run/radios-state.json DXB_RUN_DIR=$TEST_TMP/run
  mkdir -p "$DXB_STATE_DIR" "$TEST_TMP/etc"
  : > "$TEST_TMP/calls"; : > "$TEST_TMP/active"
  systemctl() { fx_systemctl "$@"; }
  fake_udevadm() { echo "udevadm $*" >> "$TEST_TMP/calls"; }
  systemd-tmpfiles() { echo "systemd-tmpfiles $*" >> "$TEST_TMP/calls"; }
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

test_presence_follows_scan() {
  radio_env; fx_scene "$DXB_SYSFS_ROOT" digirig; dxb_radio_scan_cache; dxb_radio_load
  dxb_radio_add radio1 '{"audio":"1","cat":"2"}' > /dev/null
  assert_ok dxb_radio_present radio1
  assert_eq "$(jq -c . <<< "$(dxb_radio_kernel_names radio1)")" '{"audio":"card1","cat":"ttyUSB0","hid":"hidraw1","ptt_serial":null}'
  rm -rf "$DXB_SYSFS_ROOT"; fx_scene "$DXB_SYSFS_ROOT" none; dxb_radio_scan_cache
  dxb_radio_present radio1; assert_eq "$?" "4"
}

test_apply_writes_rules_syncs_rigctld_and_state() {
  radio_env; fx_scene "$DXB_SYSFS_ROOT" digirig; dxb_radio_scan_cache; dxb_radio_load
  dxb_radio_add radio1 '{"audio":"1","cat":"2"}' > /dev/null
  assert_ok dxb_radio_apply
  assert_file_contains "$DXB_UDEV_RULES_FILE" 'ATTR{id}="RADIO1"'
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl start rigctld@radio1"
  assert_eq "$(jq -r '.radios.radio1.present' "$DXB_RADIOS_STATE")" "true"
  assert_eq "$(jq -r '.radios.radio1.rigctld' "$DXB_RADIOS_STATE")" "active"
  assert_eq "$(jq -r '.radios.radio1.kernel.audio' "$DXB_RADIOS_STATE")" "card1"
  : > "$TEST_TMP/calls"
  assert_ok dxb_radio_apply
  assert_not_contains "$(cat "$TEST_TMP/calls")" "udevadm"     # unchanged: no reload
  assert_not_contains "$(cat "$TEST_TMP/calls")" "start"
}

test_apply_hotplug_stops_absent_and_skips_udev() {
  radio_env; fx_scene "$DXB_SYSFS_ROOT" digirig; dxb_radio_scan_cache; dxb_radio_load
  dxb_radio_add radio1 '{"audio":"1","cat":"2"}' > /dev/null; dxb_radio_apply > /dev/null
  rm -rf "$DXB_SYSFS_ROOT"; fx_scene "$DXB_SYSFS_ROOT" none; rm -f "$DXB_UDEV_RULES_FILE"; : > "$TEST_TMP/calls"
  assert_ok dxb_radio_apply hotplug
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl stop rigctld@radio1"
  assert_not_contains "$(cat "$TEST_TMP/calls")" "udevadm"
  [[ -f $DXB_UDEV_RULES_FILE ]] && _fail "hotplug must not regenerate udev rules"
  assert_eq "$(jq -r '.radios.radio1.present' "$DXB_RADIOS_STATE")" "false"
}

test_apply_rewires_owner_when_inputs_change() {
  radio_env; fx_scene "$DXB_SYSFS_ROOT" digirig; dxb_radio_scan_cache; dxb_radio_load
  dxb_radio_add radio1 '{"audio":"1","cat":"2"}' > /dev/null
  REWIRED=''; dxb_app_rewire() { REWIRED+="$1;"; return 0; }
  dxb_radio_apply > /dev/null; assert_eq "$REWIRED" ""            # no owner: nothing to rewire
  _dxb_radio_set_owner radio1 fakeapp > /dev/null
  dxb_radio_apply > /dev/null; assert_eq "$REWIRED" "radio1;"    # owner set, hash new
  dxb_radio_apply > /dev/null; assert_eq "$REWIRED" "radio1;"    # unchanged: not again
  dxb_radio_set radio1 '{"baud":9600}' > /dev/null
  dxb_radio_apply > /dev/null; assert_eq "$REWIRED" "radio1;radio1;"
  dxb_app_rewire() { return 7; }
  dxb_radio_set radio1 '{"baud":4800}' > /dev/null
  dxb_radio_apply > /dev/null; assert_eq "$?" "7"
  source "$DXB_LIB/radio.sh"     # restore the real dxb_app_rewire (Task 8) for later tests in this process
}

test_apply_removes_stale_rigctld() {
  radio_env; fx_scene "$DXB_SYSFS_ROOT" digirig; dxb_radio_scan_cache; dxb_radio_load
  dxb_radio_add radio1 '{"audio":"1","cat":"2"}' > /dev/null; dxb_radio_apply > /dev/null
  dxb_radio_remove radio1 > /dev/null; : > "$TEST_TMP/calls"
  assert_ok dxb_radio_apply
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl stop rigctld@radio1"
  assert_eq "$(jq -c '.radios' "$DXB_RADIOS_STATE")" "{}"
}

test_apply_marks_wiring_hash_atomically() {
  radio_env; fx_scene "$DXB_SYSFS_ROOT" digirig; dxb_radio_scan_cache; dxb_radio_load
  dxb_radio_add radio1 '{"audio":"1","cat":"2"}' > /dev/null
  _dxb_radio_set_owner radio1 fakeapp > /dev/null
  dxb_app_rewire() { return 0; }
  assert_ok dxb_radio_apply
  local h; h=$(dxb_radio_wire_hash "$(dxb_radio_get radio1)")
  assert_eq "$(jq -r '.radios.radio1.wired_hash' "$DXB_RADIOS_STATE")" "$h"
  jq empty "$DXB_RADIOS_STATE" 2> /dev/null; assert_eq "$?" "0"
  [[ -z $(find "$(dirname "$DXB_RADIOS_STATE")" -maxdepth 1 -name '*.dxbtmp*') ]] || _fail "leftover .dxbtmp file"
  source "$DXB_LIB/radio.sh"     # restore the real dxb_app_rewire (Task 8) for later tests in this process
}

test_mark_wired_fails_when_state_parent_is_not_a_directory() {
  radio_env
  local blocker=$TEST_TMP/blocker
  : > "$blocker"
  DXB_RADIOS_STATE=$blocker/radios-state.json
  _dxb_radio_mark_wired radio1 deadbeef; assert_eq "$?" "6"
}

test_add_rejects_radio_with_no_pinned_function() {
  radio_env; fx_scene "$DXB_SYSFS_ROOT" digirig; dxb_radio_scan_cache; dxb_radio_load
  local err; err=$(dxb_radio_add e '{"label":"nothing"}' 2>&1); assert_eq "$?" "2"
  assert_contains "$err" "e: needs at least one pinned function"
}

fake_apps() {
  export DXB_APPS_DIR=$TEST_TMP/apps; mkdir -p "$DXB_APPS_DIR"; : > "$TEST_TMP/appcalls"
  cat > "$DXB_APPS_DIR/alpha.sh" <<'EOF'
app_alpha_unit() { echo alpha.service; }
app_alpha_wire() { echo "alpha wire $1" >> "$TEST_TMP/appcalls"; return "${ALPHA_WIRE_RC:-0}"; }
app_alpha_unwire() { echo "alpha unwire $1" >> "$TEST_TMP/appcalls"; }
app_alpha_needs_service_restart() { echo no; }
app_alpha_wait_ready() { echo "alpha ready" >> "$TEST_TMP/appcalls"; return "${ALPHA_READY_RC:-0}"; }
EOF
  cat > "$DXB_APPS_DIR/beta.sh" <<'EOF'
app_beta_unit() { echo beta.service; }
app_beta_wire() { echo "beta wire $1" >> "$TEST_TMP/appcalls"; return "${BETA_WIRE_RC:-0}"; }
app_beta_unwire() { echo "beta unwire $1" >> "$TEST_TMP/appcalls"; }
app_beta_needs_service_restart() { echo yes; }
EOF
}
appcalls() { tr '\n' ';' < "$TEST_TMP/appcalls"; }
two_radios() {
  fx_scene "$DXB_SYSFS_ROOT" two-digirigs; dxb_radio_scan_cache; dxb_radio_load
  dxb_radio_add r1 '{"audio":"3","cat":"4"}' > /dev/null; dxb_radio_add r2 '{"audio":"1","cat":"2"}' > /dev/null
}

test_claim_starts_wires_and_records_owner() {
  radio_env; fake_apps; two_radios
  assert_ok dxb_radio_claim r1 alpha
  assert_eq "$(appcalls)" "alpha ready;alpha wire r1;"
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl start alpha.service"
  assert_eq "$(jq -r '.owner' <<< "$(dxb_radio_get r1)")" "alpha"
  assert_eq "$(jq -r '.radios.r1.wired_hash' "$DXB_RADIOS_STATE")" "$(dxb_radio_wire_hash "$(dxb_radio_get r1)")"
  : > "$TEST_TMP/calls"; : > "$TEST_TMP/appcalls"
  assert_ok dxb_radio_claim r1 alpha                                 # same owner: re-wire only
  assert_eq "$(appcalls)" "alpha wire r1;"
  assert_not_contains "$(cat "$TEST_TMP/calls")" "start"
}

test_claim_hands_over_and_stops_idle_old_owner() {
  radio_env; fake_apps; two_radios
  dxb_radio_claim r1 alpha > /dev/null; dxb_radio_claim r2 alpha > /dev/null
  : > "$TEST_TMP/calls"; : > "$TEST_TMP/appcalls"
  assert_ok dxb_radio_claim r1 beta
  assert_eq "$(appcalls)" "alpha unwire r1;beta wire r1;"
  assert_not_contains "$(cat "$TEST_TMP/calls")" "stop alpha.service"   # alpha still owns r2
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl restart beta.service"  # needs_service_restart yes
  : > "$TEST_TMP/calls"
  assert_ok dxb_radio_claim r2 beta
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl stop alpha.service"
  assert_eq "$(jq -r '[.radios[].owner] | join(",")' <<< "$DXB_RADIOS")" "beta,beta"
}

test_claim_failure_rolls_back_to_released() {
  radio_env; fake_apps; two_radios
  ALPHA_WIRE_RC=7
  dxb_radio_claim r1 alpha; assert_eq "$?" "5"
  assert_eq "$(jq -r '.owner' <<< "$(dxb_radio_get r1)")" ""
  assert_contains "$(appcalls)" "alpha unwire r1;"
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl stop alpha.service"
  unset ALPHA_WIRE_RC; ALPHA_READY_RC=1
  dxb_radio_claim r1 alpha; assert_eq "$?" "5"
  unset ALPHA_READY_RC
}

test_claim_errors_absent_radio_unknown_app() {
  radio_env; fake_apps; two_radios
  dxb_radio_claim nope alpha; assert_eq "$?" "3"
  dxb_radio_claim r1 gamma; assert_eq "$?" "3"
  rm -rf "$DXB_SYSFS_ROOT"; fx_scene "$DXB_SYSFS_ROOT" none; dxb_radio_scan_cache
  dxb_radio_claim r1 alpha; assert_eq "$?" "4"
}

test_release_unwires_and_stops_when_idle() {
  radio_env; fake_apps; two_radios
  dxb_radio_claim r1 alpha > /dev/null; : > "$TEST_TMP/calls"; : > "$TEST_TMP/appcalls"
  assert_ok dxb_radio_release r1
  assert_eq "$(appcalls)" "alpha unwire r1;"
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl stop alpha.service"
  assert_eq "$(jq -r '.owner' <<< "$(dxb_radio_get r1)")" ""
  assert_ok dxb_radio_release r1                                      # already released: no-op
  assert_not_contains "$(cat "$TEST_TMP/calls")" "rigctld"           # rigctld untouched by hand-over
}

test_names_wiring_skips_wire_but_starts_unit() {
  radio_env; fake_apps; two_radios
  dxb_radio_set r1 '{"wiring":"names"}' > /dev/null
  assert_ok dxb_radio_claim r1 alpha
  assert_eq "$(appcalls)" "alpha ready;"
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl start alpha.service"
}

test_app_list_and_rewire() {
  radio_env; fake_apps; two_radios
  assert_eq "$(dxb_app_list | tr '\n' ' ')" "alpha beta "
  dxb_radio_claim r1 alpha > /dev/null; : > "$TEST_TMP/appcalls"
  assert_ok dxb_app_rewire r1
  assert_eq "$(appcalls)" "alpha wire r1;"
  dxb_app_rewire r2; assert_eq "$?" "0"                               # no owner: nothing to do
}

test_claim_warns_when_wiring_hash_cannot_be_recorded() {
  radio_env; fake_apps; two_radios
  local blocker=$TEST_TMP/blocker
  : > "$blocker"
  DXB_RADIOS_STATE=$blocker/radios-state.json
  dxb_radio_claim r1 alpha 2> "$TEST_TMP/stderr"; assert_eq "$?" "0"
  assert_eq "$(jq -r '.owner' <<< "$(dxb_radio_get r1)")" "alpha"
  assert_contains "$(cat "$TEST_TMP/stderr")" "could not record the wiring hash"
}

test_claim_failure_after_handover_leaves_radio_released() {
  radio_env; fake_apps; two_radios
  unset ALPHA_WIRE_RC
  dxb_radio_claim r1 alpha > /dev/null                                # alpha owns only r1
  : > "$TEST_TMP/calls"; : > "$TEST_TMP/appcalls"
  BETA_WIRE_RC=7
  dxb_radio_claim r1 beta; assert_eq "$?" "5"
  assert_eq "$(appcalls)" "alpha unwire r1;beta wire r1;beta unwire r1;"
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl stop alpha.service"   # idle after losing r1
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl stop beta.service"   # rollback
  assert_eq "$(jq -r '.owner' <<< "$(dxb_radio_get r1)")" ""
  unset BETA_WIRE_RC
}

test_add_and_set_drop_the_rig_model_without_a_cat_pin() {
  radio_env; fx_scene "$DXB_SYSFS_ROOT" ic705; dxb_radio_scan_cache; dxb_radio_load
  # the IC-705 profile carries model 3085, but audio alone gives rigctld no CAT port to drive
  assert_ok dxb_radio_add hf '{"audio":"1"}'
  assert_eq "$(jq -r '.rig.model' <<< "$(dxb_radio_get hf)")" "1"
  assert_ok dxb_radio_add hf2 '{"audio":"1","cat":"1"}'
  assert_eq "$(jq -r '.rig.model' <<< "$(dxb_radio_get hf2)")" "3085"     # a CAT pin keeps the model
  dxb_radio_set hf2 '{"cat":"none"}' 2> "$TEST_TMP/stderr" > /dev/null
  assert_eq "$(jq -r '.rig.model' <<< "$(dxb_radio_get hf2)")" "1"
  assert_contains "$(cat "$TEST_TMP/stderr")" "dummy model"
}

test_apply_holds_a_lock_while_it_runs_and_frees_it_afterwards() {
  radio_env; fx_scene "$DXB_SYSFS_ROOT" digirig; dxb_radio_scan_cache; dxb_radio_load
  dxb_radio_add radio1 '{"audio":"1","cat":"2"}' > /dev/null
  # probes the lock from inside the apply: udev's hotplug apply must wait there, not interleave
  dxb_rigctld_sync() { flock -n "$DXB_RUN_DIR/apply.lock" true; echo "$?" > "$TEST_TMP/lockprobe"; return 0; }
  assert_ok dxb_radio_apply
  source "$DXB_LIB/rigctld.sh"     # restore the real dxb_rigctld_sync for later tests in this process
  [[ -f $DXB_RUN_DIR/apply.lock ]] || _fail "apply did not create $DXB_RUN_DIR/apply.lock"
  assert_eq "$(cat "$TEST_TMP/lockprobe")" "1"
  assert_ok flock -n "$DXB_RUN_DIR/apply.lock" true      # nothing running now: the lock is free again
}

test_apply_reports_the_pending_alsa_id_until_the_card_is_renamed() {
  radio_env; fx_scene "$DXB_SYSFS_ROOT" digirig; dxb_radio_scan_cache; dxb_radio_load
  dxb_radio_add radio1 '{"audio":"1","cat":"2"}' > /dev/null
  dxb_radio_apply 2> "$TEST_TMP/stderr"
  assert_contains "$(cat "$TEST_TMP/stderr")" "radio1: audio id takes effect on replug or reboot"
  assert_eq "$(jq -r '.radios.radio1.alsa_id' "$DXB_RADIOS_STATE")" "Device"
  # the udev rule renamed the card (or it was replugged): nothing pending any more
  printf 'RADIO1\n' > "$DXB_SYSFS_ROOT/$FX_USB_BASE/1-1.3/1-1.3:1.0/sound/card1/id"
  dxb_radio_apply 2> "$TEST_TMP/stderr"
  assert_not_contains "$(cat "$TEST_TMP/stderr")" "takes effect on replug"
  assert_eq "$(jq -r '.radios.radio1.alsa_id' "$DXB_RADIOS_STATE")" "RADIO1"
}

test_apply_leaves_alsa_id_null_for_a_radio_with_no_audio_pin() {
  radio_env; fx_scene "$DXB_SYSFS_ROOT" digirig; dxb_radio_scan_cache; dxb_radio_load
  dxb_radio_add cat1 '{"cat":"2"}' > /dev/null
  dxb_radio_apply 2> "$TEST_TMP/stderr"
  assert_eq "$(jq -r '.radios.cat1.alsa_id' "$DXB_RADIOS_STATE")" "null"
  assert_not_contains "$(cat "$TEST_TMP/stderr")" "takes effect on replug"
}
