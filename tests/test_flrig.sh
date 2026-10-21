#!/usr/bin/env bash
# flrig as a radio's rig control: its module, rigctld's environment, the radio record and apply.
# (Prefix fr_.)
# shellcheck disable=SC1091,SC2034,SC2317
source "$DXB_LIB/common.sh"
source "$DXB_LIB/config.sh"
source "$DXB_LIB/gps.sh"
source "$DXB_LIB/radio.sh"
source "$DXB_LIB/radio_udev.sh"
source "$DXB_LIB/rigctld.sh"
source "$DXB_LIB/update.sh"
source "$DXB_LIB/inifile.sh"
source "$DXB_LIB/apps.sh"
source "$DXB_ROOT/tests/fixtures/apps.sh"
# fr_real_module: the real flrig module, with nothing left over from a stand-in loaded by another test
fr_real_module() { local f; for f in $(declare -F | awk '$3 ~ /^app_flrig_/ {print $3}'); do unset -f "$f"; done; source "$DXB_LIB/apps/flrig.sh"; }
[[ ! -f $DXB_LIB/apps/flrig.sh ]] || fr_real_module

# ---- the module: the port in the transceiver's own preferences file ---------------------------
# fr_env [XCVR_LINE]: the two-radio program environment and flrig's first-run flrig.prefs for both radios
fr_env() {
  fr_real_module
  fx_apps_env
  AP_INSTALLED='flrig=2.0.05-1'
  local r f
  for r in radio1 radio2; do
    f=$(app_flrig_settings_file "$r")
    mkdir -p "${f%/*}"; cp "$DXB_ROOT/tests/fixtures/apps/flrig.prefs" "$f"
  done
}
# fr_xcvr RADIO NAME: flrig.prefs of RADIO names NAME as its transceiver
fr_xcvr() { dxb_prefs_set "$(app_flrig_settings_file "$1")" xcvr_name "$2"; }
fr_dir() { local f; f=$(app_flrig_settings_file "$1"); echo "${f%/*}"; }

test_flrig_module_facts() {
  fr_env
  assert_eq "$(app_flrig_kind) $(app_flrig_session) $(app_flrig_label) $(app_flrig_audio) $(app_flrig_screen)" "rigcontrol yes flrig alsa yes"
  assert_eq "$(app_flrig_wire_each_start)" "yes"
  assert_eq "$(app_flrig_settings_file radio2)" "$DXB_APPS_HOME/.flrig-radio2/flrig.prefs"
  assert_eq "$(app_flrig_command radio2 1)" $'flrig\n--config-dir\n'"$DXB_APPS_HOME/.flrig-radio2/"
  assert_eq "$(app_flrig_unit radio2)" "dxberry-screen@flrig-radio2.service"
  assert_ok app_flrig_installed
  AP_INSTALLED=''; app_flrig_installed; assert_eq "$?" "1"
}

test_flrig_wire_default_transceiver_none() {
  fr_env
  cp "$(app_flrig_settings_file radio1)" "$TEST_TMP/before"
  assert_ok app_flrig_wire radio1
  local f; f="$(fr_dir radio1)/NONE.prefs"
  assert_eq "$(dxb_prefs_get "$f" xmlport)" "12345"
  assert_eq "$(dxb_prefs_get "$f" version)" "2.0.05"
  # a new transceiver file has the header flrig's own files have
  assert_eq "$(head -5 "$f")" $'; FLTK preferences file format 1.0\n; vendor: w1hkj.com\n; application: NONE\n\n[.]'
  # flrig.prefs itself is never written
  assert_eq "$(cmp "$TEST_TMP/before" "$(app_flrig_settings_file radio1)"; echo $?)" "0"
  # and a second wiring changes nothing
  cp "$f" "$TEST_TMP/after1"; assert_ok app_flrig_wire radio1
  assert_eq "$(cmp "$TEST_TMP/after1" "$f"; echo $?)" "0"
}
test_flrig_wire_without_flrig_prefs_uses_none() {
  fr_env; rm -f "$(app_flrig_settings_file radio1)"
  assert_ok app_flrig_wire radio1
  assert_eq "$(dxb_prefs_get "$(fr_dir radio1)/NONE.prefs" xmlport)" "12345"
}
test_flrig_wire_names_the_transceiver_file() {
  fr_env; fr_xcvr radio2 IC-7300
  assert_ok app_flrig_wire radio2
  local f; f="$(fr_dir radio2)/IC-7300.prefs"
  assert_eq "$(dxb_prefs_get "$f" xmlport)" "12346"
  assert_eq "$(dxb_prefs_get "$f" version)" "2.0.05"
  assert_eq "$(sed -n 3p "$f")" "; application: IC-7300"
  [[ ! -e $(fr_dir radio2)/NONE.prefs ]] || _fail "NONE.prefs written for a named transceiver"
}
test_flrig_wire_changes_only_the_port_of_an_existing_file() {
  fr_env; fr_xcvr radio1 IC-7300
  local f; f="$(fr_dir radio1)/IC-7300.prefs"
  printf '; FLTK preferences file format 1.0\n; vendor: w1hkj.com\n; application: IC-7300\n\n[.]\n\nversion:1.9.0\nxmlport:4000\nserial_port:/dev/ttyUSB0\nbaud:4\n' > "$f"
  assert_ok app_flrig_wire radio1
  assert_eq "$(diff <(printf '; FLTK preferences file format 1.0\n; vendor: w1hkj.com\n; application: IC-7300\n\n[.]\n\nversion:1.9.0\nxmlport:4000\nserial_port:/dev/ttyUSB0\nbaud:4\n') "$f" | grep -c '^[<>]')" "2"
  assert_eq "$(dxb_prefs_get "$f" xmlport)" "12345"
  assert_eq "$(dxb_prefs_get "$f" version)" "1.9.0"           # the operator's version is kept
  assert_eq "$(dxb_prefs_get "$f" serial_port)" "/dev/ttyUSB0"
  assert_eq "$(dxb_prefs_get "$f" baud)" "4"
}
test_flrig_wire_adds_xmlport_to_a_file_with_a_version_only() {
  fr_env; fr_xcvr radio1 FT-991A
  local f; f="$(fr_dir radio1)/FT-991A.prefs"
  printf '; FLTK preferences file format 1.0\n; vendor: w1hkj.com\n; application: FT-991A\n\n[.]\n\nversion:2.0.05\nbaud:5\n' > "$f"
  assert_ok app_flrig_wire radio1
  assert_eq "$(dxb_prefs_get "$f" xmlport)" "12345"
  assert_eq "$(dxb_prefs_get "$f" baud)" "5"
  assert_eq "$(grep -c '^version:' "$f")" "1"
}
test_flrig_wire_refuses_a_transceiver_name_that_is_a_path() {
  fr_env
  local n
  for n in '../evil' 'a/b' '.hidden' '..'; do
    fr_xcvr radio1 "$n"
    assert_ok app_flrig_wire radio1
    assert_eq "$(dxb_prefs_get "$(fr_dir radio1)/NONE.prefs" xmlport)" "12345"
  done
  [[ ! -e $(fr_dir radio1)/../evil.prefs && ! -e $(fr_dir radio1)/.hidden.prefs && ! -e $(fr_dir radio1)/..prefs ]] || _fail "a bad name made a file"
}
test_flrig_wire_version_falls_back_to_2_0() {
  fr_env; AP_INSTALLED=''
  assert_ok app_flrig_wire radio1
  assert_eq "$(dxb_prefs_get "$(fr_dir radio1)/NONE.prefs" version)" "2.0"
}
test_flrig_wire_fails_when_the_directory_cannot_be_made() {
  fr_env; printf x > "$TEST_TMP/blocker"
  DXB_APPS_HOME=$TEST_TMP/blocker
  app_flrig_wire radio1 2> /dev/null; assert_eq "$?" "6"
}

# ---- rigctld ---------------------------------------------------------------------------------
FR_DIGIRIG='{"cat":{"path":"usb-0:1.4:1.0"},"ptt_serial":null,"ptt":{"method":"rigctld"},"rig":{"model":1,"baud":57600,"ptt_type":"RTS"},"rigctld_port":4532}'
FR_IC7300='{"cat":{"path":"usb-0:1.2:1.2"},"ptt_serial":null,"ptt":{"method":"rigctld"},"rig":{"model":3073,"baud":115200,"ptt_type":"RIG"},"rigctld_port":4534}'
test_flrig_rigctld_env_for_flrig_control() {
  local r
  r=$(jq -c '.rig.control = "flrig"' <<< "$FR_DIGIRIG")
  assert_eq "$(dxb_rigctld_env radio1 "$r")" $'MODEL=4\nPORT=4532\nRIG_ARGS=-r 127.0.0.1:12345\nPTT_ARGS='
  r=$(jq -c '.rig.control = "flrig"' <<< "$FR_IC7300")
  assert_eq "$(dxb_rigctld_env radio2 "$r")" $'MODEL=4\nPORT=4534\nRIG_ARGS=-r 127.0.0.1:12346\nPTT_ARGS=-P RIG'
  assert_not_contains "$(dxb_rigctld_env radio2 "$r")" "dxberry-cat"
  assert_not_contains "$(dxb_rigctld_env radio2 "$r")" "-cat"
}
test_flrig_rigctld_env_keeps_cm108_and_gpio_ptt() {
  local r
  r=$(jq -c '.rig.control = "flrig" | .ptt.method = "cm108"' <<< "$FR_IC7300")
  assert_eq "$(dxb_rigctld_env radio2 "$r" session)" $'MODEL=4\nPORT=4534\nRIG_ARGS=-r 127.0.0.1:12346\nPTT_ARGS=-P CM108 -p /dev/dxberry/radio2-hid'
  r=$(jq -c '.rig.control = "flrig" | .ptt.method = "gpio" | .ptt.gpio_line = 17' <<< "$FR_IC7300")
  assert_eq "$(dxb_rigctld_env radio2 "$r" session)" $'MODEL=4\nPORT=4534\nRIG_ARGS=-r 127.0.0.1:12346\nPTT_ARGS=-P GPIO -p 17'
}
test_flrig_rigctld_env_without_control_is_unchanged() {
  assert_eq "$(dxb_rigctld_env radio1 "$FR_DIGIRIG")" $'MODEL=1\nPORT=4532\nRIG_ARGS=-r /dev/dxberry/radio1-cat -s 57600\nPTT_ARGS=-P RTS -p /dev/dxberry/radio1-cat'
  assert_eq "$(dxb_rigctld_env hf "$FR_IC7300")" $'MODEL=3073\nPORT=4534\nRIG_ARGS=-r /dev/dxberry/hf-cat -s 115200\nPTT_ARGS=-P RIG'
  assert_eq "$(dxb_rigctld_env hf "$(jq -c '.rig.control = "hamlib"' <<< "$FR_IC7300")")" "$(dxb_rigctld_env hf "$FR_IC7300")"
}

# ---- the radio record and apply ---------------------------------------------------------------
fr_radio_env() {
  local f; for f in $(declare -F | awk '$3 ~ /^app_flrig_/ {print $3}'); do unset -f "$f"; done
  export DXB_STATE_DIR=$TEST_TMP/state DXB_LOG_FILE=$TEST_TMP/state/log DXB_SYSFS_ROOT=$TEST_TMP/sys \
    DXB_RADIO_PROFILES=$DXB_ROOT/provision/share/radio-profiles.tsv DXB_RADIOS_FILE=$TEST_TMP/state/radios.json \
    DXB_UDEV_RULES_FILE=$TEST_TMP/etc/70.rules DXB_MODPROBE_FILE=$TEST_TMP/etc/dxberry-audio.conf DXB_UDEVADM=fr_noop \
    DXB_RIGCTLD_RUN_DIR=$TEST_TMP/run/rigctld DXB_SYSTEMD_DIR=$TEST_TMP/systemd DXB_TMPFILES_DIR=$TEST_TMP/tmpfiles \
    DXB_RADIOS_STATE=$TEST_TMP/run/radios-state.json DXB_RUN_DIR=$TEST_TMP/run DXB_WIRED_FILE=$TEST_TMP/state/wired.json \
    DXB_AMIXER=fr_noop DXB_ALSACTL=fr_noop DXB_APPS_DIR=$TEST_TMP/apps
  unset DXB_SCREENS_DIR DXB_SESSIONS_DIR
  mkdir -p "$DXB_STATE_DIR" "$TEST_TMP/etc" "$DXB_APPS_DIR"
  : > "$TEST_TMP/calls"; : > "$TEST_TMP/active"
  systemctl() { fx_systemctl "$@"; }
  systemd-tmpfiles() { :; }
  DXB_STATUS_LINES=(); DXB_FAILED_STEPS=()
  # the flrig module as apply meets it: a session program that is not an owner
  cat > "$DXB_APPS_DIR/flrig.sh" <<'MOD'
app_flrig_kind() { echo rigcontrol; }
app_flrig_session() { echo yes; }
app_flrig_installed() { return "${FR_MISSING:-0}"; }
app_flrig_unit() { dxb_app_session_unit flrig "$1"; }
MOD
  FR_MISSING=0
  fx_scene "$DXB_SYSFS_ROOT" two-digirigs; dxb_radio_scan_cache; dxb_radio_load
  dxb_radio_add r1 '{"audio":"3","cat":"4"}' > /dev/null
}
fr_noop() { :; }
FR_UNIT=dxberry-screen@flrig-r1.service

test_flrig_radio_set_stores_the_control_and_refuses_junk() {
  fr_radio_env
  local h0; h0=$(dxb_radio_wire_hash "$(dxb_radio_get r1)")
  assert_ok dxb_radio_set r1 '{"rig_control":"flrig"}'
  assert_eq "$(jq -r '.rig.control' <<< "$(dxb_radio_get r1)")" "flrig"
  assert_ok dxb_radio_set r1 '{"rig_control":"hamlib"}'
  # hamlib is the default: the key goes, so the wiring hash is what it was before flrig
  assert_eq "$(jq -r '.rig | has("control")' <<< "$(dxb_radio_get r1)")" "false"
  assert_eq "$(dxb_radio_wire_hash "$(dxb_radio_get r1)")" "$h0"
  dxb_radio_set r1 '{"rig_control":"fldigi"}' 2> /dev/null; assert_eq "$?" "2"
  assert_eq "$(jq -r '.rig | has("control")' <<< "$(dxb_radio_get r1)")" "false"      # the refused value was not kept
  # a record that never had the key stays valid, and the key is not invented
  dxb_radio_add r2 '{"audio":"1","cat":"2"}' > /dev/null
  assert_eq "$(jq -r '.rig | has("control")' <<< "$(dxb_radio_get r2)")" "false"
}
test_flrig_control_changes_the_wiring_hash() {
  fr_radio_env
  local before after
  before=$(dxb_radio_wire_hash "$(dxb_radio_get r1)")
  dxb_radio_set r1 '{"rig_control":"flrig"}' > /dev/null
  after=$(dxb_radio_wire_hash "$(dxb_radio_get r1)")
  [[ $before != "$after" ]] || _fail "switching rig control did not change the wiring hash"
}
test_flrig_apply_starts_the_session_and_rigctld_follows() {
  fr_radio_env; dxb_radio_set r1 '{"rig_control":"flrig"}' > /dev/null
  assert_ok dxb_radio_apply
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl enable $FR_UNIT"
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl --no-block start $FR_UNIT"
  assert_file_contains "$DXB_RIGCTLD_RUN_DIR/r1.env" "MODEL=4"
  assert_file_contains "$DXB_RIGCTLD_RUN_DIR/r1.env" "RIG_ARGS=-r 127.0.0.1:12345"
}
test_flrig_apply_with_flrig_not_installed_warns_once_and_continues() {
  fr_radio_env; dxb_radio_set r1 '{"rig_control":"flrig"}' > /dev/null
  FR_MISSING=1
  dxb_radio_apply 2> "$TEST_TMP/err"; assert_eq "$?" "0"
  assert_eq "$(grep -c 'flrig is not installed' "$TEST_TMP/err")" "1"
  assert_not_contains "$(cat "$TEST_TMP/calls")" "$FR_UNIT"
  assert_file_contains "$DXB_RIGCTLD_RUN_DIR/r1.env" "MODEL=4"      # the rest of apply ran
}
test_flrig_apply_stops_the_session_when_the_radio_is_absent() {
  fr_radio_env; dxb_radio_set r1 '{"rig_control":"flrig"}' > /dev/null
  dxb_radio_apply > /dev/null; : > "$TEST_TMP/calls"
  fx_unplug "$DXB_SYSFS_ROOT" r1-audio
  assert_ok dxb_radio_apply hotplug
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl stop $FR_UNIT"
  assert_not_contains "$(cat "$TEST_TMP/calls")" "disable $FR_UNIT"          # stopped, still enabled: it comes back with the radio
  assert_not_contains "$(cat "$TEST_TMP/calls")" "start $FR_UNIT"
  [[ ! -e $(_dxb_session_marker flrig r1) ]] || _fail "a waiting marker was written for flrig"
  : > "$TEST_TMP/calls"; fx_replug "$DXB_SYSFS_ROOT" r1-audio
  assert_ok dxb_radio_apply hotplug
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl --no-block start $FR_UNIT"
}
test_flrig_apply_stops_and_disables_after_switching_back_to_hamlib() {
  fr_radio_env; dxb_radio_set r1 '{"rig_control":"flrig"}' > /dev/null
  dxb_radio_apply > /dev/null
  # what enabling the unit leaves behind
  mkdir -p "$DXB_SYSTEMD_DIR/multi-user.target.wants"; : > "$DXB_SYSTEMD_DIR/multi-user.target.wants/$FR_UNIT"
  echo "$FR_UNIT" >> "$TEST_TMP/active"; echo enabled > "$TEST_TMP/enabled-$FR_UNIT"
  dxb_radio_set r1 '{"rig_control":"hamlib"}' > /dev/null; : > "$TEST_TMP/calls"
  assert_ok dxb_radio_apply
  local calls; calls=$(cat "$TEST_TMP/calls")
  assert_contains "$calls" "systemctl stop $FR_UNIT"
  assert_contains "$calls" "systemctl disable $FR_UNIT"
  assert_not_contains "$calls" "start $FR_UNIT"
  assert_contains "$calls" "systemctl restart rigctld@r1.service"            # rigctld takes the CAT port back
  # and flrig is stopped before rigctld is restarted
  assert_eq "$(grep -nE "systemctl (stop $FR_UNIT|restart rigctld@r1)" "$TEST_TMP/calls" | head -1 | grep -c "$FR_UNIT")" "1"
  assert_file_contains "$DXB_RIGCTLD_RUN_DIR/r1.env" "MODEL=1"
  # a radio that never had flrig is not asked about it again
  : > "$TEST_TMP/calls"; rm -f "$DXB_SYSTEMD_DIR/multi-user.target.wants/$FR_UNIT"
  assert_ok dxb_radio_apply
  assert_not_contains "$(cat "$TEST_TMP/calls")" "dxberry-screen@flrig"
}
test_flrig_apply_does_not_enable_an_enabled_unit_again() {
  fr_radio_env; dxb_radio_set r1 '{"rig_control":"flrig"}' > /dev/null
  echo enabled > "$TEST_TMP/enabled-$FR_UNIT"
  assert_ok dxb_radio_apply
  assert_not_contains "$(cat "$TEST_TMP/calls")" "systemctl enable $FR_UNIT"
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl --no-block start $FR_UNIT"
}
test_flrig_apply_leaves_radios_without_flrig_alone() {
  fr_radio_env
  assert_ok dxb_radio_apply
  assert_not_contains "$(cat "$TEST_TMP/calls")" "dxberry-screen@flrig"      # not even a query
}

test_flrig_cannot_own_a_radio() {
  fr_radio_env
  dxb_radio_claim r1 flrig 2> "$TEST_TMP/err"; assert_eq "$?" "3"
  assert_contains "$(cat "$TEST_TMP/err")" "flrig cannot own a radio"
  assert_eq "$(jq -r .owner <<< "$(dxb_radio_get r1)")" ""
}
test_flrig_cli_takes_rig_control() {
  assert_eq "$( source "$DXB_ROOT/provision/bin/dxberry-radio"; parse_opts --rig-control flrig | jq -c . )" '{"rig_control":"flrig"}'
  assert_contains "$( source "$DXB_ROOT/provision/bin/dxberry-radio"; usage )" "--rig-control"
}

test_flrig_cli_refuses_flrig_as_an_owner_and_a_bogus_control() {
  cli_env      # test_dxberry_radio.sh's command harness
  cat > "$DXB_APPS_DIR/flrig.sh" <<'MOD'
app_flrig_kind() { echo rigcontrol; }
app_flrig_session() { echo yes; }
app_flrig_unit() { dxb_app_session_unit flrig "$1"; }
MOD
  cli add radio1 --audio 1 --cat 2 > /dev/null
  cli claim radio1 flrig; assert_eq "$?" "3"
  assert_contains "$(cat "$TEST_TMP/err")" "flrig cannot own a radio"
  cli set radio1 --rig-control bogus; assert_eq "$?" "2"
  cli set radio1 --rig-control flrig; assert_eq "$?" "0"
  assert_eq "$(jq -r '.radios.radio1.rig.control' "$DXB_STATE_DIR/radios.json")" "flrig"
}
