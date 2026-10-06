#!/usr/bin/env bash
# shellcheck disable=SC1091,SC2034,SC2317
source "$DXB_ROOT/tests/fixtures/sysfs.sh"

cli_env() {
  export DXB_STATE_DIR=$TEST_TMP/state DXB_LOG_FILE=$TEST_TMP/state/log DXB_SYSFS_ROOT=$TEST_TMP/sys DXB_LIB=$DXB_ROOT/provision/lib \
    DXB_TEMPLATES=$DXB_ROOT/provision/templates DXB_SHARE=$DXB_ROOT/provision/share DXB_UDEV_RULES_FILE=$TEST_TMP/etc/70.rules \
    DXB_MODPROBE_FILE=$TEST_TMP/etc/audio.conf DXB_RIGCTLD_RUN_DIR=$TEST_TMP/run/rigctld DXB_SYSTEMD_DIR=$TEST_TMP/systemd \
    DXB_TMPFILES_DIR=$TEST_TMP/tmpfiles DXB_RADIOS_STATE=$TEST_TMP/run/radios-state.json DXB_APPS_DIR=$TEST_TMP/apps \
    DXB_BOOT_DIR=$TEST_TMP/boot DXB_GPSD_DEFAULT=$TEST_TMP/etc/gpsd DXB_CHRONY_DROPIN=$TEST_TMP/etc/chrony.conf DXB_ZONEINFO_DIR=$TEST_TMP/nozone \
    DXB_RADIOS_FILE=$TEST_TMP/state/radios.json DXB_RUN_DIR=$TEST_TMP/run DXB_WIRED_FILE=$TEST_TMP/state/wired.json DXB_AMIXER=amixer DXB_ALSACTL=alsactl
  mkdir -p "$DXB_STATE_DIR" "$TEST_TMP/etc" "$DXB_APPS_DIR" "$DXB_BOOT_DIR"; : > "$TEST_TMP/calls"; : > "$TEST_TMP/active"
  cat > "$DXB_APPS_DIR/alpha.sh" <<'EOF'
app_alpha_unit() { echo alpha.service; }
app_alpha_wire() { echo "alpha wire $1" >> "$TEST_TMP/calls"; }
app_alpha_unwire() { echo "alpha unwire $1" >> "$TEST_TMP/calls"; }
app_alpha_needs_service_restart() { echo no; }
EOF
  fx_scene "$DXB_SYSFS_ROOT" digirig
}
# cli_run ARGS...: run the command in a subshell with stubs; output left to the caller, exit code returned.
cli_run() {
  (
    source "$DXB_ROOT/provision/bin/dxberry-radio"     # first: the libraries define the real dxb_require_root
    dxb_require_root() { :; }
    systemctl() { fx_systemctl "$@"; }
    udevadm() { echo "udevadm $*" >> "$TEST_TMP/calls"; }
    systemd-tmpfiles() { :; }
    rigctl() { printf '145390000\nFM\n'; }
    gpspipe() { :; }
    amixer() { echo "amixer $*" >> "$TEST_TMP/calls"; }
    alsactl() { :; }
    timeout() { shift; "$@"; }
    main "$@"
  )
}
# cli ARGS...: cli_run with stdout to $TEST_TMP/out and stderr to $TEST_TMP/err.
cli() { cli_run "$@" > "$TEST_TMP/out" 2> "$TEST_TMP/err"; }
# cli_to TAG ARGS...: cli_run with its own $TEST_TMP/TAG.out and TAG.err, for two commands at once.
cli_to() { local tag=$1; shift; cli_run "$@" > "$TEST_TMP/$tag.out" 2> "$TEST_TMP/$tag.err"; }
# cli_wait_for FILE: wait up to five seconds for FILE to appear (a background command reached a point).
cli_wait_for() { local i; for (( i = 0; i < 100; i++ )); do [[ -e $1 ]] && return 0; sleep 0.05; done; return 1; }
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

test_cli_status_shows_the_current_alsa_card_id() {
  cli_env
  cli add radio1 --audio 1 --cat 2 > /dev/null
  assert_contains "$(cat "$TEST_TMP/err")" "radio1: audio id takes effect on replug or reboot"
  assert_ok cli status --json
  assert_eq "$(jq -r '.radios.radio1.alsa_id' "$TEST_TMP/out")" "Device"
  assert_ok cli status
  assert_contains "$(out)" "alsa:Device"
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

test_cli_json_mode_confirms_the_commands_that_print_nothing() {
  cli_env
  cli add radio1 --audio 1 --cat 2 > /dev/null
  assert_ok cli apply --json
  assert_eq "$(jq -c '[.ok, .warnings]' "$TEST_TMP/out")" '[true,[]]'
  assert_ok cli hotplug --json
  assert_eq "$(jq -c '[.ok, .warnings]' "$TEST_TMP/out")" '[true,[]]'
  assert_ok cli release radio1 --json
  assert_eq "$(jq -c '[.ok, .warnings]' "$TEST_TMP/out")" '[true,[]]'
  assert_ok cli remove radio1 --json
  assert_eq "$(jq -c '[.ok, .warnings]' "$TEST_TMP/out")" '[true,[]]'
  assert_ok cli apply                       # without --json the commands stay silent
  assert_eq "$(out)" ""
}

# release and remove answer only {"ok":true}, so that answer carries the run's warnings too: a
# channel the application would not delete must still reach the page.
test_cli_release_and_remove_answers_carry_the_runs_warnings() {
  cli_env
  cli add radio1 --audio 1 --cat 2 > /dev/null
  assert_ok cli claim radio1 alpha
  # as app_graywolf_unwire does when Graywolf refuses the DELETE (the later definition wins)
  cat >> "$DXB_APPS_DIR/alpha.sh" <<'APP'
app_alpha_unwire() { dxb_warn "could not delete alpha channel $1"; }
APP
  assert_ok cli release radio1 --json
  assert_eq "$(jq -c '[.ok, .warnings]' "$TEST_TMP/out")" '[true,["could not delete alpha channel radio1"]]'
  assert_ok cli claim radio1 alpha
  assert_ok cli remove radio1 --json
  assert_eq "$(jq -c '[.ok, .warnings]' "$TEST_TMP/out")" '[true,["could not delete alpha channel radio1"]]'
}

test_cli_gps_without_daemon() {
  cli_env
  assert_ok cli gps
  assert_eq "$(out)" "no receiver"
  assert_ok cli gps --json
  assert_eq "$(out)" '{"fix":0,"receiver":false}'
}

test_cli_help_and_bare_usage() {
  cli_env
  assert_ok cli -h
  assert_contains "$(out)" "claim NAME APP"
  assert_contains "$(out)" "scan"
  cli; assert_eq "$?" "2"
  assert_contains "$(cat "$TEST_TMP/err")" "claim NAME APP"
}

test_cli_add_rejects_flag_shaped_option_value() {
  cli_env
  cli add radio1 --audio 1 --cat 2 --label --wiring names
  assert_eq "$?" "2"
  assert_contains "$(cat "$TEST_TMP/err")" "--label needs a value"
  assert_ok cli add radio1 --audio 1 --cat 2 --label ""
}

# The console pins by port path: a hotplug between reading the scan and saving the form can
# renumber the scan, but a path always names the same function.
test_cli_add_takes_port_paths_as_selectors() {
  cli_env
  assert_ok cli add radio1 --audio usb-0:1.3:1.0 --cat usb-0:1.4:1.0 --label TM-V71
  # the same record the scan-index form gives: the DigiRig profile's defaults and its implied HID pin
  assert_eq "$(jq -r '.radios.radio1 | [.profile, .audio.path, .cat.path, .hid.path, .ptt.method, .rig.ptt_type] | join(" ")' "$DXB_STATE_DIR/radios.json")" \
    "0d8c:013c usb-0:1.3:1.0 usb-0:1.4:1.0 usb-0:1.3:1.3 rigctld RTS"
}

test_cli_port_path_selector_errors_and_set() {
  cli_env
  cli add radio1 --audio usb-0:1.9:1.0; assert_eq "$?" "4"
  assert_contains "$(cat "$TEST_TMP/err")" "no audio function is plugged in at usb-0:1.9:1.0"
  cli add radio1 --audio usb-0:1.4:1.0; assert_eq "$?" "4"     # that path is a serial port, not a sound card
  cli add radio1 --audio sideways; assert_eq "$?" "2"
  assert_contains "$(cat "$TEST_TMP/err")" "port path"
  cli add radio1 --audio 1 --cat 2 > /dev/null
  assert_ok cli set radio1 --cat none
  assert_eq "$(jq -c '.radios.radio1.cat' "$DXB_STATE_DIR/radios.json")" "null"
  assert_ok cli set radio1 --cat usb-0:1.4:1.0
  assert_eq "$(jq -r '.radios.radio1.cat.path' "$DXB_STATE_DIR/radios.json")" "usb-0:1.4:1.0"
}

# cli_rigctl ARGS: rigctl as the radio tests need it - Hamlib's model list for -l, else a frequency and mode
cli_rigctl() { if [[ ${1:-} == -l ]]; then cat "$DXB_ROOT/tests/fixtures/rigctl-list.txt"; else printf '145390000\nFM\n'; fi; }
cli_rigctl_garbage() { echo "rigctl: something else entirely"; }

test_cli_models_lists_hamlibs_rigs() {
  local DXB_RIGCTL=cli_rigctl
  cli_env
  assert_ok cli models --json
  assert_eq "$(jq 'length' "$TEST_TMP/out")" "5"
  assert_eq "$(jq -c '.[] | select(.model == 3073)' "$TEST_TMP/out")" '{"model":3073,"mfg":"Icom","name":"IC-7300","status":"Stable"}'
  # a blank name stays blank instead of swallowing the next column
  assert_eq "$(jq -c '.[] | select(.model == 4)' "$TEST_TMP/out")" '{"model":4,"mfg":"FLRig","name":"","status":"Stable"}'
  assert_ok cli models
  assert_contains "$(out)" "3085  Icom IC-705"
}

test_cli_models_fails_cleanly_without_a_listing() {
  local DXB_RIGCTL=false
  cli_env
  cli models --json; assert_eq "$?" "6"
  assert_contains "$(cat "$TEST_TMP/err")" "rigctl -l"
  DXB_RIGCTL=cli_rigctl_garbage
  cli models --json; assert_eq "$?" "6"
  assert_eq "$(out)" ""
}

test_cli_status_lists_unpinned_candidates_and_apps() {
  cli_env
  rm -rf "$DXB_SYSFS_ROOT"; fx_scene "$DXB_SYSFS_ROOT" two-digirigs
  cat > "$DXB_APPS_DIR/beta.sh" <<'EOF'
app_beta_unit() { echo beta.service; }
app_beta_label() { echo "Beta Modem"; }
EOF
  assert_ok cli status --json
  assert_eq "$(jq -r '[.candidates[].port] | join(" ")' "$TEST_TMP/out")" "usb-0:1.1 usb-0:1.2 usb-0:1.3 usb-0:1.4"
  assert_eq "$(jq -c '.apps' "$TEST_TMP/out")" '[{"name":"alpha","label":"alpha"},{"name":"beta","label":"Beta Modem"}]'
  assert_eq "$(jq -c '.warnings' "$TEST_TMP/out")" '[]'
  # pinning the first DigiRig (codec on 1.1, CP2102 on 1.2) takes both of its candidates off the list
  assert_ok cli add radio1 --audio usb-0:1.1:1.0 --cat usb-0:1.2:1.0
  assert_ok cli status --json
  assert_eq "$(jq -r '[.candidates[].port] | join(" ")' "$TEST_TMP/out")" "usb-0:1.3 usb-0:1.4"
  assert_eq "$(jq -r '.candidates[0] | .index, .defaults.ptt' "$TEST_TMP/out" | tr '\n' ' ')" "3 rigctld "
  assert_ok cli status
  assert_contains "$(out)" "plugged in, not set up: 3  usb-0:1.3  DigiRig Mobile [0d8c:013c]"
}

# cockpit.spawn drops a successful command's stderr, so a --json answer carries the run's warnings.
test_cli_json_answers_carry_the_runs_warnings() {
  cli_env
  # no CAT pin: the DigiRig profile's RTS keying has no serial line behind it, so add warns and uses NONE
  assert_ok cli add radio1 --audio 1 --cat none --json
  assert_contains "$(jq -r '.warnings[]' "$TEST_TMP/out")" "ptt_type RTS needs a serial pin; set to NONE"
  assert_eq "$(jq -r '.radios.radio1.rig.ptt_type' "$TEST_TMP/out")" "NONE"
  assert_ok cli set radio1 --label quiet --json
  assert_eq "$(jq -c '.warnings' "$TEST_TMP/out")" '[]'
}

# Two radio changes at once must take turns on the record. Here release radio1 is still unwiring
# (slowly, as Graywolf's API calls are) when claim radio2 starts: without a record lock the claim
# saves radio2's new owner, then the release saves its older copy of the record over it and stops
# the application radio2 was just given to.
test_cli_radio_changes_take_turns_on_the_record() {
  command -v flock > /dev/null 2>&1 || return 0   # without flock the lock is only a warning
  local pid rc
  cli_env
  rm -rf "$DXB_SYSFS_ROOT"; fx_scene "$DXB_SYSFS_ROOT" two-digirigs
  cli add radio1 --audio usb-0:1.1:1.0 --cat usb-0:1.2:1.0 > /dev/null
  cli add radio2 --audio usb-0:1.3:1.0 --cat usb-0:1.4:1.0 > /dev/null
  assert_ok cli claim radio1 alpha
  # the later definition wins when the module is sourced
  cat >> "$DXB_APPS_DIR/alpha.sh" <<'APP'
app_alpha_unwire() { : > "$TEST_TMP/unwiring"; sleep 2; echo "alpha unwire $1" >> "$TEST_TMP/calls"; }
APP
  cli_to release release radio1 &
  pid=$!
  cli_wait_for "$TEST_TMP/unwiring" || _fail "release radio1 never reached alpha's unwire"
  cli_to claim claim radio2 alpha; rc=$?
  wait "$pid"; assert_eq "$?" "0"
  assert_eq "$rc" "0"
  assert_eq "$(jq -r '.radios.radio1.owner + "," + .radios.radio2.owner' "$DXB_RADIOS_FILE")" ",alpha"
  grep -qx alpha.service "$TEST_TMP/active" || _fail "alpha.service was stopped although radio2 is wired to it"
}

# A change that cannot get the record within DXB_RADIO_LOCK_WAIT seconds fails with exit 6 and
# leaves the record alone; reading (status) and apply never wait for it.
test_cli_a_radio_change_gives_up_while_another_holds_the_record() {
  command -v flock > /dev/null 2>&1 || return 0
  local pid DXB_RADIO_LOCK_WAIT=1
  cli_env
  cli add radio1 --audio 1 --cat 2 --label before > /dev/null
  mkdir -p "$DXB_RUN_DIR"
  ( exec 8> "$DXB_RUN_DIR/record.lock"; flock 8; : > "$TEST_TMP/held"; exec sleep 10 ) &
  pid=$!
  cli_wait_for "$TEST_TMP/held" || _fail "the background holder never took the record lock"
  cli set radio1 --label after --json; assert_eq "$?" "6"
  assert_contains "$(cat "$TEST_TMP/err")" "another radio change is still running; try again when it has finished"
  assert_eq "$(jq -r '.radios.radio1.label' "$DXB_RADIOS_FILE")" "before"
  cli release radio1; assert_eq "$?" "6"
  assert_ok cli status --json
  assert_ok cli apply
  kill "$pid" 2> /dev/null; wait "$pid" 2> /dev/null
  assert_ok cli set radio1 --label after
}
