#!/usr/bin/env bash
# shellcheck disable=SC1091,SC2034,SC2317
source "$DXB_ROOT/tests/fixtures/sysfs.sh"

cli_env() {
  export DXB_STATE_DIR=$TEST_TMP/state DXB_LOG_FILE=$TEST_TMP/state/log DXB_SYSFS_ROOT=$TEST_TMP/sys DXB_LIB=$DXB_ROOT/provision/lib \
    DXB_TEMPLATES=$DXB_ROOT/provision/templates DXB_SHARE=$DXB_ROOT/provision/share DXB_UDEV_RULES_FILE=$TEST_TMP/etc/70.rules \
    DXB_MODPROBE_FILE=$TEST_TMP/etc/audio.conf DXB_RIGCTLD_RUN_DIR=$TEST_TMP/run/rigctld DXB_SYSTEMD_DIR=$TEST_TMP/systemd \
    DXB_TMPFILES_DIR=$TEST_TMP/tmpfiles DXB_RADIOS_STATE=$TEST_TMP/run/radios-state.json DXB_APPS_DIR=$TEST_TMP/apps \
    DXB_BOOT_DIR=$TEST_TMP/boot DXB_GPSD_DEFAULT=$TEST_TMP/etc/gpsd DXB_CHRONY_DROPIN=$TEST_TMP/etc/chrony.conf DXB_ZONEINFO_DIR=$TEST_TMP/nozone \
    DXB_RADIOS_FILE=$TEST_TMP/state/radios.json
  mkdir -p "$DXB_STATE_DIR" "$TEST_TMP/etc" "$DXB_APPS_DIR" "$DXB_BOOT_DIR"; : > "$TEST_TMP/calls"; : > "$TEST_TMP/active"
  cat > "$DXB_APPS_DIR/alpha.sh" <<'EOF'
app_alpha_unit() { echo alpha.service; }
app_alpha_wire() { echo "alpha wire $1" >> "$TEST_TMP/calls"; }
app_alpha_unwire() { echo "alpha unwire $1" >> "$TEST_TMP/calls"; }
app_alpha_needs_service_restart() { echo no; }
EOF
  fx_scene "$DXB_SYSFS_ROOT" digirig
}
# cli ARGS...: run the command in a subshell with stubs; stdout to $TEST_TMP/out, exit code returned.
cli() {
  (
    source "$DXB_ROOT/provision/bin/dxberry-radio"     # first: the libraries define the real dxb_require_root
    dxb_require_root() { :; }
    systemctl() { fx_systemctl "$@"; }
    udevadm() { echo "udevadm $*" >> "$TEST_TMP/calls"; }
    systemd-tmpfiles() { :; }
    rigctl() { printf '145390000\nFM\n'; }
    gpspipe() { :; }
    timeout() { shift; "$@"; }
    main "$@"
  ) > "$TEST_TMP/out" 2> "$TEST_TMP/err"
}
out() { cat "$TEST_TMP/out"; }

test_cli_scan_table_and_json() {
  cli_env
  assert_ok cli scan
  assert_contains "$(out)" "DigiRig Mobile"
  assert_contains "$(out)" "usb-0:1.3"
  assert_ok cli scan --json
  assert_eq "$(jq -r '.[0].functions[0].kernel' "$TEST_TMP/out")" "card1"
}

test_cli_add_applies_and_status_shows_radio() {
  cli_env
  assert_ok cli add radio1 --audio 1 --cat 2 --label "TM-V71"
  assert_file_contains "$DXB_UDEV_RULES_FILE" 'ATTR{id}="RADIO1"'
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl start rigctld@radio1"
  assert_ok cli status --json
  assert_eq "$(jq -r '.radios.radio1.present, .radios.radio1.rigctld, .radios.radio1.freq, .radios.radio1.mode' "$TEST_TMP/out" | tr '\n' ' ')" "true active 145390000 FM "
  assert_ok cli status
  assert_contains "$(out)" "radio1"
  assert_contains "$(out)" "145390000"
}

test_cli_usage_and_error_codes() {
  cli_env
  cli; assert_eq "$?" "2"
  cli frobnicate; assert_eq "$?" "2"
  cli add; assert_eq "$?" "2"
  cli add radio1 --audio 9; assert_eq "$?" "2"
  cli set nope --label x; assert_eq "$?" "3"
  cli claim nope alpha; assert_eq "$?" "3"
  cli add radio1 --audio 1 --cat 2 > /dev/null
  cli claim radio1 nosuchapp; assert_eq "$?" "3"
  rm -rf "$DXB_SYSFS_ROOT"; fx_scene "$DXB_SYSFS_ROOT" none
  cli claim radio1 alpha; assert_eq "$?" "4"
}

test_cli_claim_release_remove() {
  cli_env
  cli add radio1 --audio 1 --cat 2 > /dev/null
  assert_ok cli claim radio1 alpha
  assert_contains "$(cat "$TEST_TMP/calls")" "alpha wire radio1"
  assert_ok cli status --json; assert_eq "$(jq -r '.radios.radio1.owner' "$TEST_TMP/out")" "alpha"
  assert_ok cli release radio1
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl stop alpha.service"
  assert_ok cli remove radio1
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl stop rigctld@radio1"
  assert_ok cli status --json; assert_eq "$(jq -c '.radios' "$TEST_TMP/out")" "{}"
}

test_cli_set_and_hotplug() {
  cli_env
  cli add radio1 --audio 1 --cat 2 > /dev/null
  assert_ok cli set radio1 --ptt cm108 --wiring names
  assert_eq "$(jq -r '.radios.radio1.ptt.method + " " + .radios.radio1.wiring' "$DXB_STATE_DIR/radios.json")" "cm108 names"
  cli set radio1 --wiring sideways; assert_eq "$?" "2"
  rm -rf "$DXB_SYSFS_ROOT"; fx_scene "$DXB_SYSFS_ROOT" none; : > "$TEST_TMP/calls"
  assert_ok cli hotplug
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl stop rigctld@radio1"
  assert_not_contains "$(cat "$TEST_TMP/calls")" "udevadm"
}

test_cli_gps_without_daemon() {
  cli_env
  assert_ok cli gps
  assert_eq "$(out)" "no fix"
  assert_ok cli gps --json
  assert_eq "$(out)" '{"fix":0}'
}
