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
    DXB_RADIOS_STATE=$TEST_TMP/run/radios-state.json DXB_RUN_DIR=$TEST_TMP/run DXB_WIRED_FILE=$TEST_TMP/state/wired.json DXB_AMIXER=fake_amixer DXB_ALSACTL=fake_alsactl
  unset DXB_SCREENS_DIR DXB_SESSIONS_DIR
  mkdir -p "$DXB_STATE_DIR" "$TEST_TMP/etc"
  : > "$TEST_TMP/calls"; : > "$TEST_TMP/active"
  systemctl() { fx_systemctl "$@"; }
  FAKE_AMIXER_NO_DB=0
  # a card with: PCM (playback volume+switch), Mic (capture-only, left alone), a bare playback
  # mute switch 'Auto Gain Control',0 and a second-index playback control 'PCM',1
  fake_amixer() {
    echo "amixer $*" >> "$TEST_TMP/calls"
    case " $* " in
      *" scontrols "*) printf "Simple mixer control 'PCM',0\nSimple mixer control 'Mic',0\nSimple mixer control 'Auto Gain Control',0\nSimple mixer control 'PCM',1\n" ;;
      *" sget PCM,0 "*|*" sget PCM,1 "*) echo "  Capabilities: pvolume pswitch pswitch-joined" ;;
      *" sget Mic,0 "*) echo "  Capabilities: cvolume cswitch" ;;
      *" sget Auto Gain Control,0 "*) echo "  Capabilities: pswitch pswitch-joined" ;;
      *" sset "*dB*) (( FAKE_AMIXER_NO_DB )) && return 1 ;;
    esac
    return 0
  }
  fake_alsactl() { echo "alsactl $*" >> "$TEST_TMP/calls"; }
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
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl --no-block start rigctld@radio1"
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
  [[ -z $(find "$(dirname "$DXB_RADIOS_STATE")" "$DXB_STATE_DIR" -maxdepth 1 -name '*.dxbtmp*') ]] || _fail "leftover .dxbtmp file"
  assert_eq "$(jq -r '.radio1' "$DXB_WIRED_FILE")" "$h"
  source "$DXB_LIB/radio.sh"     # restore the real dxb_app_rewire (Task 8) for later tests in this process
}

test_mark_wired_fails_when_the_marks_parent_is_not_a_directory() {
  radio_env; fx_scene "$DXB_SYSFS_ROOT" digirig; dxb_radio_scan_cache; dxb_radio_load
  dxb_radio_add radio1 '{"audio":"1","cat":"2"}' > /dev/null
  local blocker=$TEST_TMP/blocker
  : > "$blocker"
  DXB_WIRED_FILE=$blocker/wired.json
  _dxb_radio_mark_wired radio1 deadbeef 2> /dev/null; assert_eq "$?" "6"
  DXB_WIRED_FILE=$TEST_TMP/state/wired.json
  DXB_RADIOS_STATE=$blocker/radios-state.json                       # only the mirror blocked: recorded, warned, 0
  _dxb_radio_mark_wired radio1 deadbeef 2> /dev/null; assert_eq "$?" "0"
  assert_eq "$(jq -r '.radio1' "$DXB_WIRED_FILE")" "deadbeef"
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
  cat > "$DXB_APPS_DIR/gamma.sh" <<'EOF'
app_gamma_kind() { echo radio; }
app_gamma_session() { echo yes; }
app_gamma_installed() { return "${GAMMA_MISSING:-0}"; }
app_gamma_multi() { echo "${GAMMA_MULTI:-yes}"; }
app_gamma_unit() { echo "dxberry-screen@gamma-$1.service"; }
app_gamma_wire() { echo "gamma wire $1" >> "$TEST_TMP/appcalls"; }
app_gamma_unwire() { echo "gamma unwire $1" >> "$TEST_TMP/appcalls"; }
app_gamma_needs_service_restart() { echo no; }
EOF
  cat > "$DXB_APPS_DIR/delta.sh" <<'EOF'
app_delta_kind() { echo rigcontrol; }
app_delta_unit() { echo delta.service; }
EOF
}
appcalls() { tr '\n' ';' < "$TEST_TMP/appcalls"; }
two_radios() {
  fx_scene "$DXB_SYSFS_ROOT" two-digirigs; dxb_radio_scan_cache; dxb_radio_load
  dxb_radio_add r1 '{"audio":"3","cat":"4"}' > /dev/null; dxb_radio_add r2 '{"audio":"1","cat":"2"}' > /dev/null
}

# ra_sc: the systemctl stand-in of the sound server tests: fx_systemctl, whose list-units lists the
# dxberry-audio@ units it has running. ra_audio_env: the sound server unit is installed, and
# DXB_SYSTEMCTL is ra_sc (call it in a subshell test). ra_line LINE: the number of LINE's first
# call (empty when it was not made).
ra_sc() {
  if [[ $1 == list-units ]]; then
    echo "systemctl $*" >> "$TEST_TMP/calls"
    grep '^dxberry-audio@' "$TEST_TMP/active" 2> /dev/null | awk '{ print $1, "loaded active running x" }'
    return 0
  fi
  fx_systemctl "$@"
}
ra_audio_env() {
  mkdir -p "$DXB_SYSTEMD_DIR" && cp "$DXB_ROOT/provision/templates/dxberry-audio@.service" "$DXB_SYSTEMD_DIR/"
  DXB_SYSTEMCTL=ra_sc
}
ra_line() { grep -nxF -- "$1" "$TEST_TMP/calls" | head -1 | cut -d: -f1; }
ra_before() {
  local a b; a=$(ra_line "$1"); b=$(ra_line "$2")
  if [[ -z $a || -z $b ]] || (( a > b )); then _fail "'$1' (call ${a:-none}) must come before '$2' (call ${b:-none})"; fi
}

# The radio apply runs each radio's sound server (design 8.1: no program starts it for a radio
# nobody owns): started for a present, unowned radio with an audio device, its earlier failures
# and start limit cleared first, and left alone once it runs.
test_apply_starts_the_sound_server_of_an_unowned_radio() (
  radio_env; ra_audio_env; two_radios
  assert_ok dxb_radio_apply
  ra_before "systemctl reset-failed dxberry-audio@r1.service" "systemctl --no-block start dxberry-audio@r1.service"
  ra_before "systemctl reset-failed dxberry-audio@r2.service" "systemctl --no-block start dxberry-audio@r2.service"
  : > "$TEST_TMP/calls"
  assert_ok dxb_radio_apply
  assert_not_contains "$(cat "$TEST_TMP/calls")" "start dxberry-audio"
)

# ... and never for a radio Graywolf (any owner that is not a session program) has, nor for one a
# session program owns (its session starts and stops the server), one with no audio device or an
# absent one
test_apply_starts_no_sound_server_where_it_must_not_run() (
  radio_env; ra_audio_env; fake_apps; two_radios
  _dxb_radio_set_owner r1 alpha > /dev/null
  _dxb_radio_set_owner r2 gamma > /dev/null
  dxb_radio_apply > /dev/null 2>&1
  assert_not_contains "$(cat "$TEST_TMP/calls")" "start dxberry-audio"
  assert_not_contains "$(cat "$TEST_TMP/calls")" "dxberry-audio@r2"
  dxb_radio_save "$(jq -c '.radios.r1.owner = "" | .radios.r1.audio = null' <<< "$DXB_RADIOS")" > /dev/null
  _dxb_radio_set_owner r2 "" > /dev/null
  fx_unplug "$DXB_SYSFS_ROOT" r2-audio
  : > "$TEST_TMP/calls"
  dxb_radio_apply > /dev/null 2>&1
  assert_not_contains "$(cat "$TEST_TMP/calls")" "start dxberry-audio"
)

# A radio that is unplugged or leaves the record has its server stopped: a replug comes back to a
# fresh server, and a removed radio's server no longer holds the card
test_apply_stops_the_sound_server_of_an_absent_or_removed_radio() (
  radio_env; ra_audio_env; two_radios
  dxb_radio_apply > /dev/null
  fx_unplug "$DXB_SYSFS_ROOT" r1-audio
  : > "$TEST_TMP/calls"
  assert_ok dxb_radio_apply hotplug
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl stop dxberry-audio@r1.service"
  assert_not_contains "$(cat "$TEST_TMP/calls")" "stop dxberry-audio@r2"
  fx_replug "$DXB_SYSFS_ROOT" r1-audio
  : > "$TEST_TMP/calls"
  assert_ok dxb_radio_apply hotplug
  ra_before "systemctl reset-failed dxberry-audio@r1.service" "systemctl --no-block start dxberry-audio@r1.service"
  dxb_radio_remove r2 > /dev/null
  : > "$TEST_TMP/calls"
  assert_ok dxb_radio_apply
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl stop dxberry-audio@r2.service"
  assert_not_contains "$(cat "$TEST_TMP/calls")" "stop dxberry-audio@r1"
)

# An unplugged radio's server is stopped whoever owns the radio: one started by a PulseAudio
# program's session (JS8Call, fldigi, QSSTV) would otherwise run on with no card after the card
# went, and the session's start after the replug would find it still running
test_apply_stops_a_session_radios_sound_server_when_it_is_unplugged() (
  radio_env; ra_audio_env; fake_apps; two_radios
  _dxb_radio_set_owner r1 gamma > /dev/null
  echo dxberry-audio@r1.service >> "$TEST_TMP/active"    # the session's prepare started it
  fx_unplug "$DXB_SYSFS_ROOT" r1-audio
  : > "$TEST_TMP/calls"
  dxb_radio_apply hotplug > /dev/null 2>&1
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl stop dxberry-audio@r1.service"
  # back again: the apply leaves the start to the session, which now starts a fresh server
  fx_replug "$DXB_SYSFS_ROOT" r1-audio
  : > "$TEST_TMP/calls"
  dxb_radio_apply hotplug > /dev/null 2>&1
  assert_not_contains "$(cat "$TEST_TMP/calls")" "start dxberry-audio@r1"
  if grep -qx dxberry-audio@r1.service "$TEST_TMP/active"; then _fail "the old server still runs after the replug"; fi
)

# A release from Graywolf (any program that is not a session program) still running for other
# radios: Graywolf lets go of the card (its restart, awaited) before the radio's server starts,
# so the server does not lose the race for the card
test_release_lets_the_program_settle_before_the_sound_server_starts() (
  radio_env; ra_audio_env; fake_apps; two_radios
  dxb_radio_apply > /dev/null
  assert_ok dxb_radio_claim r1 alpha
  assert_ok dxb_radio_claim r2 alpha
  : > "$TEST_TMP/calls"; : > "$TEST_TMP/appcalls"
  assert_ok dxb_radio_release r1
  ra_before "systemctl try-restart alpha.service" "systemctl --no-block start dxberry-audio@r1.service"
  assert_contains "$(appcalls)" "alpha ready"
)

# An update's apply runs before the setup installs the unit: nothing is asked of a unit that is
# not there yet (provision_station syncs the servers once it has installed it)
test_apply_leaves_the_sound_server_alone_until_its_unit_is_installed() (
  radio_env; two_radios
  DXB_SYSTEMCTL=ra_sc
  assert_ok dxb_radio_apply
  assert_not_contains "$(cat "$TEST_TMP/calls")" "dxberry-audio"
)

# A hand-over runs no apply. A claim by Graywolf (any program that is not a session program)
# stops the radio's server before the program starts, so the program finds its card free; a
# release, or a claim that failed and left the radio released, starts it again. A session
# program's claim leaves the server to its session.
test_claim_and_release_hand_the_sound_card_over() (
  radio_env; ra_audio_env; fake_apps; two_radios
  dxb_radio_apply > /dev/null
  : > "$TEST_TMP/calls"
  assert_ok dxb_radio_claim r1 alpha
  ra_before "systemctl stop dxberry-audio@r1.service" "systemctl start alpha.service"
  : > "$TEST_TMP/calls"
  assert_ok dxb_radio_release r1
  ra_before "systemctl reset-failed dxberry-audio@r1.service" "systemctl --no-block start dxberry-audio@r1.service"
  : > "$TEST_TMP/calls"
  ALPHA_WIRE_RC=1 dxb_radio_claim r1 alpha > /dev/null 2>&1; assert_eq "$?" "5"
  ra_before "systemctl stop dxberry-audio@r1.service" "systemctl --no-block start dxberry-audio@r1.service"
  assert_eq "$(jq -r '.radios.r1.owner' <<< "$DXB_RADIOS")" ""
  : > "$TEST_TMP/calls"
  dxb_radio_claim r2 gamma > /dev/null 2>&1
  assert_not_contains "$(cat "$TEST_TMP/calls")" "dxberry-audio@r2"
)

test_claim_starts_wires_and_records_owner() {
  radio_env; fake_apps; two_radios
  assert_ok dxb_radio_claim r1 alpha
  assert_eq "$(appcalls)" "alpha ready;alpha wire r1;"
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl start alpha.service"
  assert_eq "$(jq -r '.owner' <<< "$(dxb_radio_get r1)")" "alpha"
  assert_eq "$(jq -r '.radios.r1.wired_hash' "$DXB_RADIOS_STATE")" "$(dxb_radio_wire_hash "$(dxb_radio_get r1)")"
  : > "$TEST_TMP/calls"; : > "$TEST_TMP/appcalls"
  assert_ok dxb_radio_claim r1 alpha                                 # same owner: re-wire only
  assert_eq "$(appcalls)" "alpha ready;alpha wire r1;"
  assert_not_contains "$(cat "$TEST_TMP/calls")" "systemctl start"
  : > "$TEST_TMP/active"; : > "$TEST_TMP/calls"
  assert_ok dxb_radio_claim r1 alpha                                 # same owner, unit stopped: start it first
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl start alpha.service"
}

# The rc2 Pi's codec came up with PCM playback at 69 % (-20 dB) on top of Graywolf's default
# -12 dB, so test transmit never keyed the radio. Wiring puts every playback control at 0 dB,
# unmuted, and saves it so the level survives a reboot. Capture controls belong to the operator.
test_wire_sets_playback_levels_and_saves_them() {
  radio_env; fake_apps; two_radios
  assert_ok dxb_radio_claim r1 alpha
  assert_contains "$(cat "$TEST_TMP/calls")" "amixer -q -c 1 sset PCM,0 playback 0dB unmute"
  assert_contains "$(cat "$TEST_TMP/calls")" "amixer -q -c 1 sset PCM,1 playback 0dB unmute"
  assert_contains "$(cat "$TEST_TMP/calls")" "amixer -q -c 1 sset Auto Gain Control,0 playback unmute"
  assert_not_contains "$(cat "$TEST_TMP/calls")" "sset Mic"
  assert_not_contains "$(cat "$TEST_TMP/calls")" "sset Auto Gain Control,0 playback 0dB"
  assert_contains "$(cat "$TEST_TMP/calls")" "alsactl store 1"
  assert_eq "$(grep -c 'sset PCM,0' "$TEST_TMP/calls")" "1"
}

test_wire_falls_back_to_full_scale_without_db_info() {
  radio_env; fake_apps; two_radios
  FAKE_AMIXER_NO_DB=1
  assert_ok dxb_radio_claim r1 alpha
  assert_contains "$(cat "$TEST_TMP/calls")" "amixer -q -c 1 sset PCM,0 playback 100% unmute"
}

# The wiring mark used to live only in the /run mirror, so every boot re-wired every owned radio
# before graywolf.service was up (the hotplug unit is ordered before it) and the unit failed.
test_wired_mark_survives_a_reboot() {
  radio_env; fake_apps; two_radios
  assert_ok dxb_radio_claim r1 alpha
  local h; h=$(dxb_radio_wire_hash "$(dxb_radio_get r1)")
  assert_eq "$(jq -r '.r1' "$DXB_STATE_DIR/wired.json")" "$h"
  rm -rf "$DXB_RUN_DIR"; : > "$TEST_TMP/appcalls"                 # reboot: tmpfs gone, record and mark kept
  assert_ok dxb_radio_apply hotplug
  assert_not_contains "$(appcalls)" "alpha wire"
  assert_eq "$(jq -r '.radios.r1.wired_hash' "$DXB_RADIOS_STATE")" "$h"
}

test_apply_skips_the_rewire_while_the_owner_is_not_running() {
  radio_env; fake_apps; two_radios
  assert_ok dxb_radio_claim r1 alpha
  dxb_radio_set r1 '{"ptt":"vox"}' > /dev/null                         # record changed: a re-wire is due
  : > "$TEST_TMP/active"; : > "$TEST_TMP/appcalls"; : > "$TEST_TMP/calls"   # ... but alpha is stopped (boot order)
  dxb_radio_apply hotplug 2> "$TEST_TMP/stderr"; assert_eq "$?" "0"
  assert_not_contains "$(appcalls)" "alpha wire"
  assert_contains "$(cat "$TEST_TMP/stderr")" "alpha is not running"
  assert_not_contains "$(cat "$TEST_TMP/calls")" "systemctl start alpha.service"   # apply never starts units
  echo alpha.service >> "$TEST_TMP/active"; : > "$TEST_TMP/appcalls"
  assert_ok dxb_radio_apply hotplug
  assert_eq "$(appcalls)" "alpha ready;alpha wire r1;"                # ready first: the owner may still be starting
  assert_eq "$(jq -r '.r1' "$DXB_STATE_DIR/wired.json")" "$(dxb_radio_wire_hash "$(dxb_radio_get r1)")"
  : > "$TEST_TMP/active"; dxb_radio_set r1 '{"ptt":"none"}' > /dev/null
  dxb_radio_apply 2> "$TEST_TMP/stderr"; assert_eq "$?" "0"          # a hand-run apply says so louder
  assert_contains "$(cat "$TEST_TMP/stderr")" "[WARN]"
  assert_contains "$(cat "$TEST_TMP/stderr")" "wiring was not updated"
}

test_apply_reports_a_failed_owner_unit_with_7() {
  radio_env; fake_apps; two_radios
  assert_ok dxb_radio_claim r1 alpha
  dxb_radio_set r1 '{"ptt":"vox"}' > /dev/null
  : > "$TEST_TMP/active"; echo alpha.service > "$TEST_TMP/failed"; : > "$TEST_TMP/appcalls"
  dxb_radio_apply hotplug 2> "$TEST_TMP/stderr"; assert_eq "$?" "7"
  assert_contains "$(cat "$TEST_TMP/stderr")" "unit has failed"
  assert_not_contains "$(appcalls)" "alpha wire"
}

test_apply_reports_an_owner_that_never_becomes_ready_with_7() {
  radio_env; fake_apps; two_radios
  assert_ok dxb_radio_claim r1 alpha
  dxb_radio_set r1 '{"ptt":"vox"}' > /dev/null; : > "$TEST_TMP/appcalls"
  ALPHA_READY_RC=1 dxb_radio_apply hotplug 2> "$TEST_TMP/stderr"; assert_eq "$?" "7"
  assert_contains "$(cat "$TEST_TMP/stderr")" "did not become ready"
  assert_not_contains "$(appcalls)" "alpha wire"
}

# The levels are derived state: every apply re-asserts them for a present, owned radio, so a
# reboot (whose hotplug apply no longer re-wires an unchanged record) still gets 0 dB and a
# fresh alsactl store under the card's final id.
test_apply_reasserts_playback_levels_without_rewiring() {
  radio_env; fake_apps; two_radios
  assert_ok dxb_radio_claim r1 alpha
  : > "$TEST_TMP/calls"; : > "$TEST_TMP/appcalls"
  assert_ok dxb_radio_apply hotplug
  assert_not_contains "$(appcalls)" "alpha wire"
  assert_contains "$(cat "$TEST_TMP/calls")" "amixer -q -c 1 sset PCM,0 playback 0dB unmute"
  assert_contains "$(cat "$TEST_TMP/calls")" "alsactl store 1"
  assert_not_contains "$(cat "$TEST_TMP/calls")" "amixer -q -c 2"    # r2 has no owner
  assert_eq "$(grep -c 'alsactl store' "$TEST_TMP/calls")" "1"
}

test_wired_marks_are_pruned_and_a_corrupt_file_is_replaced() {
  radio_env; fake_apps; two_radios
  mkdir -p "$DXB_STATE_DIR"; printf '{"ghost":"deadbeef"}\n' > "$DXB_STATE_DIR/wired.json"
  assert_ok dxb_radio_claim r1 alpha
  assert_eq "$(jq -c 'keys' "$DXB_STATE_DIR/wired.json")" '["r1"]'
  assert_eq "$(stat -c %a "$DXB_STATE_DIR/wired.json")" "600"
  dxb_radio_claim r2 alpha > /dev/null
  printf '[1,2]\n' > "$DXB_STATE_DIR/wired.json"                     # corrupt: not an object
  assert_ok dxb_radio_remove r1
  assert_eq "$(cat "$DXB_STATE_DIR/wired.json")" '{}'               # replaced, never truncated to nothing
  printf 'not json\n' > "$DXB_STATE_DIR/wired.json"
  assert_ok dxb_radio_claim r2 alpha
  assert_eq "$(jq -c 'keys' "$DXB_STATE_DIR/wired.json")" '["r2"]'
}

test_remove_drops_the_wired_mark() {
  radio_env; fake_apps; two_radios
  dxb_radio_claim r1 alpha > /dev/null; dxb_radio_claim r2 alpha > /dev/null
  assert_ok dxb_radio_remove r1
  assert_eq "$(jq -c 'keys' "$DXB_STATE_DIR/wired.json")" '["r2"]'
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
  dxb_radio_claim r1 nosuch; assert_eq "$?" "3"
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
  assert_eq "$(dxb_app_list | tr '\n' ' ')" "alpha beta delta gamma "
  dxb_radio_claim r1 alpha > /dev/null; : > "$TEST_TMP/appcalls"
  assert_ok dxb_app_rewire r1
  assert_eq "$(appcalls)" "alpha wire r1;"
  dxb_app_rewire r2; assert_eq "$?" "0"                               # no owner: nothing to do
}

test_claim_warns_when_wiring_hash_cannot_be_recorded() {
  radio_env; fake_apps; two_radios
  local blocker=$TEST_TMP/blocker
  : > "$blocker"
  DXB_WIRED_FILE=$blocker/wired.json
  dxb_radio_claim r1 alpha 2> "$TEST_TMP/stderr"; assert_eq "$?" "0"
  assert_eq "$(jq -r '.owner' <<< "$(dxb_radio_get r1)")" "alpha"
  assert_contains "$(cat "$TEST_TMP/stderr")" "could not record the wiring hash"
}

test_claim_only_warns_softly_when_the_mirror_cannot_be_refreshed() {
  radio_env; fake_apps; two_radios
  local blocker=$TEST_TMP/blocker
  : > "$blocker"
  DXB_RADIOS_STATE=$blocker/radios-state.json
  dxb_radio_claim r1 alpha 2> "$TEST_TMP/stderr"; assert_eq "$?" "0"
  assert_eq "$(jq -r '.r1' "$DXB_STATE_DIR/wired.json")" "$(dxb_radio_wire_hash "$(dxb_radio_get r1)")"   # the mark itself is recorded
  assert_contains "$(cat "$TEST_TMP/stderr")" "status mirror could not be refreshed"
  assert_not_contains "$(cat "$TEST_TMP/stderr")" "could not record the wiring hash"
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

test_mark_wired_rebuilds_an_unparsable_mirror_before_merging() {
  radio_env; fx_scene "$DXB_SYSFS_ROOT" digirig; dxb_radio_scan_cache; dxb_radio_load
  dxb_radio_add radio1 '{"audio":"1","cat":"2"}' > /dev/null
  mkdir -p "$(dirname "$DXB_RADIOS_STATE")"; printf 'not json\n' > "$DXB_RADIOS_STATE"
  assert_ok _dxb_radio_mark_wired radio1 deadbeef
  assert_eq "$(jq -r '.radios.radio1.wired_hash' "$DXB_RADIOS_STATE")" "deadbeef"
  assert_eq "$(jq -r '.radios.radio1.present' "$DXB_RADIOS_STATE")" "true"
  assert_eq "$(jq -r '.radios.radio1.kernel.audio' "$DXB_RADIOS_STATE")" "card1"
  assert_eq "$(jq -r '.generated != null' "$DXB_RADIOS_STATE")" "true"
}

test_claim_session_app_enables_and_starts_its_radio_unit() {
  radio_env; fake_apps; two_radios
  assert_ok dxb_radio_claim r1 gamma
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl enable dxberry-screen@gamma-r1.service"
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl --no-block start dxberry-screen@gamma-r1.service"
  assert_not_contains "$(appcalls)" "gamma wire"   # a session is wired by its own prepare step
  assert_eq "$(jq -r '.owner' <<< "$(dxb_radio_get r1)")" "gamma"
}

test_handover_from_session_app_stops_only_that_radios_unit() {
  radio_env; fake_apps; two_radios
  dxb_radio_claim r1 gamma > /dev/null; dxb_radio_claim r2 gamma > /dev/null; : > "$TEST_TMP/calls"
  assert_ok dxb_radio_claim r1 alpha
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl disable --now dxberry-screen@gamma-r1.service"
  assert_not_contains "$(cat "$TEST_TMP/calls")" "dxberry-screen@gamma-r2.service"
}

test_claim_refuses_uninstalled_and_non_radio_programs() {
  radio_env; fake_apps; two_radios
  GAMMA_MISSING=1 dxb_radio_claim r1 gamma 2> "$TEST_TMP/err"; assert_eq "$?" "3"
  assert_contains "$(cat "$TEST_TMP/err")" "gamma is not installed"
  dxb_radio_claim r1 delta 2> "$TEST_TMP/err"; assert_eq "$?" "3"
  assert_contains "$(cat "$TEST_TMP/err")" "delta cannot own a radio"
}

test_claim_refuses_second_copy_of_single_copy_program() {
  radio_env; fake_apps; two_radios
  GAMMA_MULTI=no; dxb_radio_claim r1 gamma > /dev/null
  dxb_radio_claim r2 gamma 2> "$TEST_TMP/err"; assert_eq "$?" "5"
  assert_contains "$(cat "$TEST_TMP/err")" "gamma runs on one radio at a time (r1)"
  assert_eq "$(jq -r '.owner' <<< "$(dxb_radio_get r2)")" ""
  unset GAMMA_MULTI
}

test_claim_session_failure_disables_unit_and_releases() {
  radio_env; fake_apps; two_radios
  DXB_SESSION_WAIT_S=0 FX_SYSTEMCTL_FAIL_START='dxberry-screen@gamma-r1.service' dxb_radio_claim r1 gamma > /dev/null 2>&1
  assert_eq "$?" "5"
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl disable --now dxberry-screen@gamma-r1.service"
  assert_eq "$(jq -r '.owner' <<< "$(dxb_radio_get r1)")" ""
}

test_apply_stops_and_restarts_sessions_by_presence() {
  radio_env; fake_apps; two_radios
  dxb_radio_claim r1 gamma > /dev/null; : > "$TEST_TMP/calls"
  echo enabled > "$TEST_TMP/enabled-dxberry-screen@gamma-r1.service"   # claim enabled it
  fx_unplug "$DXB_SYSFS_ROOT" r1-audio            # the r1 card disappears
  dxb_radio_apply hotplug > /dev/null 2>&1
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl stop dxberry-screen@gamma-r1.service"
  assert_not_contains "$(cat "$TEST_TMP/calls")" "disable"
  [[ -f $DXB_RUN_DIR/sessions/gamma-r1.waiting ]] || _fail "the stopped session was not marked as waiting for its radio"
  : > "$TEST_TMP/calls"; fx_replug "$DXB_SYSFS_ROOT" r1-audio
  dxb_radio_apply hotplug > /dev/null 2>&1
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl --no-block start dxberry-screen@gamma-r1.service"
  assert_not_contains "$(appcalls)" "gamma wire"
  [[ -e $DXB_RUN_DIR/sessions/gamma-r1.waiting ]] && _fail "the waiting marker outlived the restart"
}

# A program the operator quit from its own menu ends its session but stays enabled; only the next
# boot or Start brings it back (ruling 9), never an apply - unless apply itself stopped it for an
# unplugged radio (the .waiting marker). A Start by hand clears a marker left behind.
# The same holds across an unplug: a session the operator had already quit was not stopped by the
# radio's absence, so it gets no marker and the replug leaves it stopped.
test_apply_leaves_a_quit_session_stopped_across_an_unplug() {
  radio_env; fake_apps; two_radios
  dxb_radio_claim r1 gamma > /dev/null
  echo enabled > "$TEST_TMP/enabled-dxberry-screen@gamma-r1.service"
  sed -i '/^dxberry-screen@gamma-r1.service$/d' "$TEST_TMP/active"   # quit from its menu
  fx_unplug "$DXB_SYSFS_ROOT" r1-audio
  dxb_radio_apply hotplug > /dev/null 2>&1
  [[ -e $DXB_RUN_DIR/sessions/gamma-r1.waiting ]] && _fail "a session that was not running must not be marked as waiting"
  fx_replug "$DXB_SYSFS_ROOT" r1-audio; : > "$TEST_TMP/calls"
  dxb_radio_apply hotplug > /dev/null 2>&1
  assert_not_contains "$(cat "$TEST_TMP/calls")" "start dxberry-screen@gamma-r1.service"
}

test_apply_leaves_a_quit_session_stopped_without_a_marker() {
  radio_env; fake_apps; two_radios
  dxb_radio_claim r1 gamma > /dev/null
  echo enabled > "$TEST_TMP/enabled-dxberry-screen@gamma-r1.service"
  sed -i '/^dxberry-screen@gamma-r1.service$/d' "$TEST_TMP/active"; : > "$TEST_TMP/calls"   # quit from its menu
  assert_ok dxb_radio_apply hotplug
  assert_not_contains "$(cat "$TEST_TMP/calls")" "start dxberry-screen@gamma-r1.service"
  mkdir -p "$DXB_RUN_DIR/sessions"; : > "$DXB_RUN_DIR/sessions/gamma-r1.waiting"
  assert_ok dxb_radio_claim r1 gamma                                   # Start by hand
  [[ -e $DXB_RUN_DIR/sessions/gamma-r1.waiting ]] && _fail "a session started by hand is no longer waiting"
}

# Graywolf -> session on a cm108 radio while Graywolf keeps another radio: its unwire only queues
# a restart, which the old process can outlive, so the claim restarts it with a blocking
# try-restart and waits for it to be ready (when the module can tell) before rigctld opens the HID.
test_handover_to_session_waits_for_the_old_owner_before_rigctld() {
  radio_env; fake_apps; two_radios
  dxb_radio_set r1 '{"ptt":"cm108"}' > /dev/null
  dxb_radio_claim r1 alpha > /dev/null; dxb_radio_claim r2 alpha > /dev/null
  dxb_radio_apply > /dev/null 2>&1                                     # rigctld@r1 up with the Graywolf env
  # the later definition wins when the module is sourced: log to the same file as systemctl
  cat >> "$DXB_APPS_DIR/alpha.sh" <<'APP'
app_alpha_unwire() { echo "alpha unwire $1" >> "$TEST_TMP/calls"; }
app_alpha_wait_ready() { echo "alpha ready" >> "$TEST_TMP/calls"; }
APP
  : > "$TEST_TMP/calls"
  assert_ok dxb_radio_claim r1 gamma
  assert_eq "$(grep -n '^alpha \|try-restart alpha.service\|restart rigctld@r1\|enable dxberry-screen@gamma-r1' "$TEST_TMP/calls" | cut -d: -f2 | tr '\n' ';')" \
    'alpha unwire r1;systemctl try-restart alpha.service;alpha ready;systemctl --no-block restart rigctld@r1.service;systemctl enable dxberry-screen@gamma-r1.service;'
  radio_env; fake_apps; two_radios
  dxb_radio_set r1 '{"ptt":"cm108"}' > /dev/null
  dxb_radio_claim r1 beta > /dev/null; dxb_radio_claim r2 beta > /dev/null
  dxb_radio_apply > /dev/null 2>&1; : > "$TEST_TMP/calls"
  assert_ok dxb_radio_claim r1 gamma
  assert_eq "$(grep -n 'try-restart beta.service\|restart rigctld@r1\|enable dxberry-screen@gamma-r1' "$TEST_TMP/calls" | cut -d: -f2 | tr '\n' ';')" \
    'systemctl try-restart beta.service;systemctl --no-block restart rigctld@r1.service;systemctl enable dxberry-screen@gamma-r1.service;'
}

test_catalog_lists_installed_radio_programs_with_kind() {
  radio_env; fake_apps
  assert_eq "$(dxb_app_catalog | jq -c 'map(select(.name == "gamma" or .name == "delta"))')" \
    '[{"name":"gamma","label":"gamma","kind":"radio","session":true,"installed":true}]'
  GAMMA_MISSING=1; assert_eq "$(dxb_app_catalog | jq -r '.[] | select(.name == "gamma") | .installed')" "false"; unset GAMMA_MISSING
}

# rigctld keys cm108 PTT only while a session program owns the radio; handing the radio to a
# Graywolf-like owner stops the session first, then gives the HID back before that owner starts.
test_session_owner_moves_cm108_ptt_into_rigctld_and_back() {
  radio_env; fake_apps; two_radios
  dxb_radio_set r1 '{"ptt":"cm108"}' > /dev/null
  assert_ok dxb_radio_claim r1 gamma
  assert_file_contains "$DXB_RIGCTLD_RUN_DIR/r1.env" "PTT_ARGS=-P CM108 -p /dev/dxberry/r1-hid"
  : > "$TEST_TMP/calls"
  assert_ok dxb_radio_claim r1 alpha
  assert_file_not_contains "$DXB_RIGCTLD_RUN_DIR/r1.env" "CM108"
  assert_eq "$(grep -n 'disable --now dxberry-screen@gamma-r1\|restart rigctld@r1\|start alpha.service' "$TEST_TMP/calls" | cut -d: -f2 | tr '\n' ';')" \
    'systemctl disable --now dxberry-screen@gamma-r1.service;systemctl --no-block restart rigctld@r1.service;systemctl start alpha.service;'
  dxb_radio_claim r1 gamma > /dev/null; : > "$TEST_TMP/calls"
  assert_ok dxb_radio_release r1
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl disable --now dxberry-screen@gamma-r1.service"
  assert_file_not_contains "$DXB_RIGCTLD_RUN_DIR/r1.env" "CM108"
  assert_eq "$(jq -r '.owner' <<< "$(dxb_radio_get r1)")" ""
}
