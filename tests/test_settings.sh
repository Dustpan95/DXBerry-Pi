#!/usr/bin/env bash
# shellcheck disable=SC1091,SC2034,SC2317
source "$DXB_LIB/common.sh"
source "$DXB_LIB/config.sh"
source "$DXB_LIB/settings.sh"

# se_env: a provisioned Pi's dxberry.txt (secrets already scrubbed) on a fake boot partition.
se_env() {
  export DXB_STATE_DIR=$TEST_TMP/state DXB_LOG_FILE=$TEST_TMP/state/log DXB_BOOT_DIR=$TEST_TMP/boot \
    DXB_ZONEINFO_DIR=$TEST_TMP/zoneinfo DXB_RUN_DIR=$TEST_TMP/run DXB_CONFIG_PREV=$TEST_TMP/state/dxberry.txt.prev
  mkdir -p "$DXB_STATE_DIR" "$DXB_BOOT_DIR" "$DXB_ZONEINFO_DIR/America" "$DXB_RUN_DIR"
  : > "$DXB_ZONEINFO_DIR/UTC"; : > "$DXB_ZONEINFO_DIR/America/Chicago"
  cat > "$DXB_BOOT_DIR/dxberry.txt" <<'EOF'
# DXBerry-Pi settings
PASSWORD=<applied>
HOSTNAME = shackpi
TIMEZONE=America/Chicago
STATIC_IP=10.0.0.90/24
GATEWAY=10.0.0.1
WIFI_SSID=Shack Net
WIFI_PASSWORD=<applied>
WIFI_COUNTRY=US
CALLSIGN=N0CALL-10
# GPS_DEVICE=auto
EOF
}
se_file() { cat "$DXB_BOOT_DIR/dxberry.txt"; }

test_settings_get_reports_values_and_never_secrets() {
  local j
  se_env
  j=$(dxb_settings_get_json "$DXB_BOOT_DIR/dxberry.txt")
  assert_eq "$(jq -r '.valid' <<< "$j")" "true"
  assert_eq "$(jq -c '.keys.HOSTNAME' <<< "$j")" '{"value":"shackpi","effective":"shackpi"}'
  assert_eq "$(jq -c '.keys.STATIC_IP' <<< "$j")" '{"value":"10.0.0.90/24","effective":"10.0.0.90/24"}'
  # unset keys show the default in effect
  assert_eq "$(jq -c '.keys.GPS_DEVICE' <<< "$j")" '{"value":"","effective":"auto"}'
  assert_eq "$(jq -c '.keys.DNS' <<< "$j")" '{"value":"","effective":"10.0.0.1"}'
  assert_eq "$(jq -c '.keys.PASSWORD' <<< "$j")" '{"secret":true,"set":true}'
  assert_eq "$(jq -c '.keys.WIFI_PASSWORD' <<< "$j")" '{"secret":true,"set":true}'
  # only the console's keys: station keys belong to Graywolf's page
  assert_eq "$(jq -r '.keys | has("CALLSIGN")' <<< "$j")" "false"
  assert_eq "$(jq -r '.keys | length' <<< "$j")" "15"
  assert_not_contains "$j" "<applied>"
}

test_settings_get_reports_an_invalid_file() {
  local j
  se_env
  printf 'PASSWORD=<applied>\nGATEWAY=10.0.0.1\nSTATIC_IP=192.168.1.5/24\n' > "$DXB_BOOT_DIR/dxberry.txt"
  j=$(dxb_settings_get_json "$DXB_BOOT_DIR/dxberry.txt")
  assert_eq "$(jq -r '.valid' <<< "$j")" "false"
  assert_contains "$(jq -r '.errors[]' <<< "$j")" "GATEWAY is not inside 192.168.1.5/24"
}

test_settings_edit_rewrites_appends_and_drops_duplicates() {
  local out
  out=$(printf '# HOSTNAME=commented\nHOSTNAME = old\nA=1\nHOSTNAME=dup\n' | dxb_settings_edit HOSTNAME new)
  assert_eq "$out" "$(printf '# HOSTNAME=commented\nHOSTNAME=new\nA=1')"
  out=$(printf 'A=1\n' | dxb_settings_edit TIMEZONE 'America/Chicago')
  assert_eq "$out" "$(printf 'A=1\nTIMEZONE=America/Chicago')"
  # an empty value unsets the key: the default applies
  out=$(printf 'STATIC_IP=10.0.0.90/24\n' | dxb_settings_edit STATIC_IP '')
  assert_eq "$out" "STATIC_IP="
  # values are literal: backslashes, quotes, spaces and & survive
  out=$(printf 'A=1\n' | dxb_settings_edit SSH_PUBKEY 'ssh-ed25519 AAA\x26& "me"')
  assert_eq "$out" "$(printf 'A=1\nSSH_PUBKEY=ssh-ed25519 AAA\\x26& "me"')"
}

test_settings_validate_reports_the_validators_messages() {
  local errs rc
  se_env
  errs=$(dxb_settings_validate "$(se_file | dxb_settings_edit GATEWAY 192.168.9.1)"); rc=$?
  assert_eq "$rc" "4"
  assert_contains "$errs" "GATEWAY is not inside 10.0.0.90/24"
  errs=$(dxb_settings_validate "$(se_file | dxb_settings_edit TIMEZONE UTC)"); rc=$?
  assert_eq "$rc" "0"
  # the caller's DXB_CFG is untouched (validation runs in a subshell)
  DXB_CFG=([HOSTNAME]=keepme)
  dxb_settings_validate "$(se_file)" > /dev/null
  assert_eq "${DXB_CFG[HOSTNAME]}" "keepme"
}

test_settings_changed_names_keys_without_values() {
  local old new out
  se_env
  old=$(se_file)
  new=$(dxb_settings_edit HOSTNAME other <<< "$old" | dxb_settings_edit WIFI_PASSWORD 'hunter2hunter2')
  out=$(dxb_settings_changed "$old" "$new")
  assert_eq "$out" "$(printf 'WIFI_PASSWORD\nHOSTNAME' | sort)"
  assert_not_contains "$out" "hunter2"
  # a station key is not a console key: not reported
  new=$(dxb_settings_edit CALLSIGN N0CALL-9 <<< "$old")
  assert_eq "$(dxb_settings_changed "$old" "$new")" ""
}

test_settings_write_keeps_the_previous_file_private() {
  local before
  se_env
  before=$(se_file)
  assert_ok dxb_settings_write "$DXB_BOOT_DIR/dxberry.txt" "$(dxb_settings_edit HOSTNAME other <<< "$before")"
  assert_file_contains "$DXB_BOOT_DIR/dxberry.txt" "HOSTNAME=other"
  assert_eq "$(cat "$DXB_CONFIG_PREV")" "$before"
  assert_eq "$(stat -c %a "$DXB_CONFIG_PREV")" "600"
  # a write that does not land is exit 6
  chmod 500 "$DXB_BOOT_DIR"
  dxb_settings_write "$DXB_BOOT_DIR/dxberry.txt" "HOSTNAME=x" 2> /dev/null; local rc=$?
  chmod 700 "$DXB_BOOT_DIR"
  if (( EUID != 0 )); then assert_eq "$rc" "6"; fi
}

test_settings_key_lists_and_value_check() {
  assert_ok dxb_settings_is_console_key HOSTNAME
  assert_fails dxb_settings_is_console_key CALLSIGN
  assert_ok dxb_settings_is_network_key WIFI_SSID
  assert_fails dxb_settings_is_network_key HOSTNAME
  assert_ok dxb_settings_is_secret WIFI_PASSWORD
  assert_fails dxb_settings_is_secret WIFI_SSID
  assert_ok dxb_settings_value_ok 'Shack Net 5G'
  assert_fails dxb_settings_value_ok "$(printf 'a\nb')"
  assert_fails dxb_settings_value_ok "$(printf 'a\tb')"
}

test_settings_temp_files_stay_private_and_are_removed() {
  local t new
  se_env
  t=$(_dxb_settings_tmp 'hunter2hunter2')
  assert_eq "${t:0:${#DXB_STATE_DIR}}" "$DXB_STATE_DIR"
  assert_eq "$(stat -c %a "$t")" "600"
  rm -f "$t"
  # the public functions built on it clean up after themselves: nothing left under the state dir
  new=$(dxb_settings_edit WIFI_PASSWORD 'hunter2hunter2' <<< "$(se_file)")
  dxb_settings_changed "$(se_file)" "$new" > /dev/null
  dxb_settings_validate "$new" > /dev/null
  assert_eq "$(find "$DXB_STATE_DIR" -maxdepth 1 -name '.settings.*' | wc -l)" "0"
}

# se_netsafe_env: se_env plus fake network files and a systemd-run that records what it arms.
se_netsafe_env() {
  se_env
  export DXB_NETSAFE_DIR=$TEST_TMP/state/network-snapshot DXB_NETSAFE_AT=$TEST_TMP/run/config-revert-at \
    DXB_NETSAFE_FILES="$TEST_TMP/etc/interfaces $TEST_TMP/etc/eth0.conf $TEST_TMP/etc/wlan0.conf $TEST_TMP/etc/wpa.conf" \
    DXB_SYSTEMD_RUN=se_systemd_run DXB_CONFIG_CMD=/opt/dxberry/bin/dxberry-config
  mkdir -p "$TEST_TMP/etc"
  echo 'source interfaces.d/*' > "$TEST_TMP/etc/interfaces"
  echo 'iface eth0 inet static' > "$TEST_TMP/etc/eth0.conf"
  ( umask 077; echo 'psk="old-secret"' > "$TEST_TMP/etc/wpa.conf" )
  : > "$TEST_TMP/calls"
}
se_systemd_run() {
  echo "systemd-run $*" >> "$TEST_TMP/calls"
  [[ ! -f $TEST_TMP/systemd-run-fails ]] || return 1
  [[ ! -f $TEST_TMP/job-start-fails || $* != *' apply'* ]] || return 1
  return 0
}

test_netsafe_snapshot_and_restore_put_every_file_back() {
  se_netsafe_env
  assert_ok dxb_netsafe_snapshot
  assert_eq "$(stat -c %a "$DXB_NETSAFE_DIR")" "700"
  assert_ok dxb_netsafe_pending
  # the change: files edited, one created, the WiFi key replaced
  echo 'iface eth0 inet dhcp' > "$TEST_TMP/etc/eth0.conf"
  echo 'iface wlan0 inet dhcp' > "$TEST_TMP/etc/wlan0.conf"
  echo 'psk="new-secret"' > "$TEST_TMP/etc/wpa.conf"
  printf 'PASSWORD=<applied>\nSTATIC_IP=\n' > "$DXB_BOOT_DIR/dxberry.txt"
  assert_ok dxb_netsafe_restore
  assert_eq "$(cat "$TEST_TMP/etc/eth0.conf")" "iface eth0 inet static"
  [[ -e $TEST_TMP/etc/wlan0.conf ]] && _fail "a file the change created must be removed on restore"
  assert_file_contains "$TEST_TMP/etc/wpa.conf" "old-secret"
  assert_eq "$(stat -c %a "$TEST_TMP/etc/wpa.conf")" "600"
  assert_file_contains "$DXB_BOOT_DIR/dxberry.txt" "STATIC_IP=10.0.0.90/24"
  assert_fails dxb_netsafe_pending
  assert_fails dxb_netsafe_restore
}

test_netsafe_arm_never_leaves_a_gap() {
  se_netsafe_env
  (
    local first second l_arm l_stop before=$TESTS_FAILED
    systemctl() { echo "systemctl $*" >> "$TEST_TMP/calls"; }
    assert_ok dxb_netsafe_arm 600
    first=$(awk '{print $1}' "$DXB_NETSAFE_AT")
    assert_contains "$first" "dxberry-config-revert-"
    assert_contains "$(cat "$TEST_TMP/calls")" "--on-active=600 --unit=$first /opt/dxberry/bin/dxberry-config revert"
    assert_ok dxb_netsafe_arm 120
    second=$(awk '{print $1}' "$DXB_NETSAFE_AT")
    [[ $second != "$first" ]] || _fail "each arm needs its own unit name"
    # the old timer is stopped only after the new one is armed
    l_arm=$(grep -n 'on-active=120' "$TEST_TMP/calls" | cut -d: -f1)
    l_stop=$(grep -n "stop $first.timer" "$TEST_TMP/calls" | cut -d: -f1)
    [[ -n $l_arm && -n $l_stop ]] && (( l_arm < l_stop )) || _fail "the new timer must be armed before the old one is stopped"
    # a failed arm keeps the timer that was there
    touch "$TEST_TMP/systemd-run-fails"
    dxb_netsafe_arm 120 2> /dev/null; assert_eq "$?" "6"
    assert_eq "$(awk '{print $1}' "$DXB_NETSAFE_AT")" "$second"
    assert_not_contains "$(cat "$TEST_TMP/calls")" "stop $second.timer"
    dxb_netsafe_disarm
    assert_contains "$(cat "$TEST_TMP/calls")" "systemctl stop $second.timer"
    [[ -e $DXB_NETSAFE_AT ]] && _fail "disarm must forget the deadline"
    exit $(( TESTS_FAILED > before ? 1 : 0 ))
  ) || TESTS_FAILED=$(( TESTS_FAILED + 1 ))
}

test_netsafe_revert_at_reports_the_deadline() {
  se_netsafe_env
  echo "dxberry-config-revert-1 1700000120" > "$DXB_NETSAFE_AT"
  assert_eq "$(dxb_netsafe_revert_at)" "1700000120"
  rm -f "$DXB_NETSAFE_AT"
  assert_eq "$(dxb_netsafe_revert_at)" ""
}

# se_cli ARGS...: run dxberry-config's main in a subshell with stubs; stdout to $TEST_TMP/out,
# stderr to $TEST_TMP/err, exit code returned. Standard input passes through (for --stdin).
# systemctl: is-active answers from $TEST_TMP/active (one unit per line).
se_cli() {
  (
    source "$DXB_ROOT/provision/bin/dxberry-config"
    dxb_require_root() { :; }
    systemctl() {
      echo "systemctl $*" >> "$TEST_TMP/calls"
      case $1 in
        is-active) grep -qx "${*: -1}" "$TEST_TMP/active" 2> /dev/null ;;
        *) return 0 ;;
      esac
    }
    journalctl() { printf 'applying: HOSTNAME\nfinished: all steps completed\n'; }
    main "$@"
  ) > "$TEST_TMP/out" 2> "$TEST_TMP/err"
}
se_out() { cat "$TEST_TMP/out"; }
# se_cli_env: se_netsafe_env plus a provisioner stub and the job files under $TEST_TMP.
se_cli_env() {
  se_netsafe_env
  # shellcheck disable=SC2031  # exported here, only ever read (never set) inside se_cli's subshell
  export DXB_CONFIG_JOB_FILE=$TEST_TMP/run/config-job.json DXB_CONFIG_RESULT=$TEST_TMP/run/config-result.json \
    DXB_CONFIG_PENDING=$TEST_TMP/run/config-pending DXB_CONFIG_REVERTED=$TEST_TMP/state/config-reverted \
    DXB_PROVISION_CMD=se_provision DXB_REBOOT_FLAG=$TEST_TMP/run/reboot-required DXB_CONFIG_LOCK_WAIT=2 \
    DXB_GW_COOKIES=$TEST_TMP/run/graywolf.cookies
  : > "$TEST_TMP/active"
}
se_provision() { echo "provision DXB_GW_UPGRADE=${DXB_GW_UPGRADE:-unset}" >> "$TEST_TMP/calls"; return "$(cat "$TEST_TMP/provision-rc" 2> /dev/null || echo 0)"; }
# se_wait_for FILE: wait up to five seconds for FILE to appear (a background command reached a
# point). `command sleep`: other test files' env helpers leave a no-op sleep() function in this
# shared shell.
se_wait_for() { local i; for (( i = 0; i < 100; i++ )); do [[ -e $1 ]] && return 0; command sleep 0.05; done; return 1; }

test_config_get_json_and_text() {
  se_cli_env
  assert_ok se_cli get --json
  assert_eq "$(jq -r '.keys.HOSTNAME.value' "$TEST_TMP/out")" "shackpi"
  assert_ok se_cli get
  assert_contains "$(se_out)" "HOSTNAME=shackpi"
  assert_contains "$(se_out)" "PASSWORD=(set)"
  assert_not_contains "$(se_out)" "<applied>"
  rm "$DXB_BOOT_DIR/dxberry.txt"
  se_cli get --json; assert_eq "$?" "4"
}

test_config_set_writes_and_starts_the_job() {
  se_cli_env
  assert_ok se_cli set HOSTNAME=newpi TIMEZONE=UTC --json
  assert_eq "$(jq -c '.changed' "$TEST_TMP/out")" '["HOSTNAME","TIMEZONE"]'
  assert_eq "$(jq -r '.network' "$TEST_TMP/out")" "false"
  assert_contains "$(jq -r '.job' "$TEST_TMP/out")" "dxberry-job-config-"
  assert_file_contains "$DXB_BOOT_DIR/dxberry.txt" "HOSTNAME=newpi"
  assert_file_contains "$DXB_BOOT_DIR/dxberry.txt" "# DXBerry-Pi settings"
  assert_contains "$(cat "$TEST_TMP/calls")" "--unit=$(jq -r '.job' "$TEST_TMP/out") --description=DXBerry settings change /opt/dxberry/bin/dxberry-config apply"
  # shellcheck disable=SC2002  # cat for a clear left-to-right read: the pending file, then squash it to one line
  assert_eq "$(cat "$DXB_CONFIG_PENDING" | tr '\n' ' ')" "HOSTNAME TIMEZONE "
  assert_eq "$(jq -r '.unit' "$DXB_CONFIG_JOB_FILE")" "$(jq -r '.job' "$TEST_TMP/out")"
  # not a network change: no snapshot, no undo timer
  assert_fails dxb_netsafe_pending
  assert_not_contains "$(cat "$TEST_TMP/calls")" "on-active"
}

test_config_set_of_the_same_values_changes_nothing() {
  se_cli_env
  assert_ok se_cli set HOSTNAME=shackpi --json
  assert_eq "$(se_out)" '{"ok":true,"changed":[],"job":null,"network":false}'
  assert_not_contains "$(cat "$TEST_TMP/calls")" "systemd-run"
}

# A key string spanning two adjacent names in the space-separated DXB_CONSOLE_KEYS list (here,
# "STATIC_IP GATEWAY") must never pass dxb_settings_is_console_key's substring check: _set_check
# rejects anything that is not itself a single valid key name before it ever gets there.
test_config_set_rejects_a_malformed_key_name() {
  se_cli_env
  se_cli set "STATIC_IP GATEWAY=192.168.9.1"; assert_eq "$?" "2"
}

test_config_set_reports_6_when_settings_cannot_be_checked() {
  local before
  se_cli_env
  before=$(se_file)
  rm -rf "$DXB_STATE_DIR"
  : > "$DXB_STATE_DIR"
  se_cli set HOSTNAME=x; assert_eq "$?" "6"
  assert_contains "$(cat "$TEST_TMP/err")" "could not check the settings"
  rm -f "$DXB_STATE_DIR"
  mkdir -p "$DXB_STATE_DIR"
  assert_eq "$(se_file)" "$before"
}

test_config_set_usage_error_never_echoes_the_argument() {
  se_cli_env
  se_cli set "hunter2hunter2"; assert_eq "$?" "2"
  assert_eq "$(cat "$TEST_TMP/err")" "expected KEY=VALUE arguments"
  assert_not_contains "$(cat "$TEST_TMP/err")" "hunter2"
}

test_config_set_waits_for_the_lock() {
  command -v flock > /dev/null 2>&1 || return 0
  local pid
  se_cli_env
  export DXB_CONFIG_LOCK_WAIT=1
  mkdir -p "$DXB_RUN_DIR"
  ( exec 8> "$DXB_RUN_DIR/config.lock"; flock 8; : > "$TEST_TMP/held"; exec sleep 10 ) &
  pid=$!
  se_wait_for "$TEST_TMP/held" || _fail "the background holder never took the settings lock"
  se_cli set HOSTNAME=blocked --json; assert_eq "$?" "5"
  assert_contains "$(cat "$TEST_TMP/err")" "in progress"
  kill "$pid" 2> /dev/null; wait "$pid" 2> /dev/null
}

test_config_set_refuses_what_it_must() {
  local before
  se_cli_env
  before=$(se_file)
  se_cli set CALLSIGN=N0CALL-9; assert_eq "$?" "2"
  assert_contains "$(cat "$TEST_TMP/err")" "Graywolf's page"
  se_cli set PASSWORD=hunter2hunter2; assert_eq "$?" "2"
  assert_contains "$(cat "$TEST_TMP/err")" "standard input"
  assert_not_contains "$(cat "$TEST_TMP/err")" "hunter2"
  se_cli set "HOSTNAME=$(printf 'a\nb')"; assert_eq "$?" "2"
  se_cli set HOSTNAME; assert_eq "$?" "2"
  se_cli set; assert_eq "$?" "2"
  se_cli set HOSTNAME=x WIFI_SSID=Other; assert_eq "$?" "2"
  assert_contains "$(cat "$TEST_TMP/err")" "on their own"
  se_cli set GATEWAY=192.168.9.1; assert_eq "$?" "4"
  assert_contains "$(cat "$TEST_TMP/err")" "GATEWAY is not inside 10.0.0.90/24"
  assert_eq "$(se_file)" "$before"
  assert_not_contains "$(cat "$TEST_TMP/calls")" "systemd-run"
}

test_config_set_takes_secrets_only_on_stdin() {
  se_cli_env
  printf 'PASSWORD=correct horse battery\n' | se_cli set --stdin --json
  assert_eq "$?" "0"
  assert_eq "$(jq -c '.changed' "$TEST_TMP/out")" '["PASSWORD"]'
  assert_file_contains "$DXB_BOOT_DIR/dxberry.txt" "PASSWORD=correct horse battery"
  assert_not_contains "$(cat "$TEST_TMP/calls")" "correct horse"
  assert_not_contains "$(cat "$TEST_TMP/out" "$TEST_TMP/err")" "correct horse"
  # stdin takes only the two secrets
  : > "$TEST_TMP/active"; rm -f "$DXB_CONFIG_JOB_FILE"
  printf 'HOSTNAME=sneaky\n' | se_cli set --stdin; assert_eq "$?" "2"
  assert_not_contains "$(cat "$TEST_TMP/err")" "sneaky"
  # without --stdin nothing is read (a caller that sends nothing never hangs)
  printf 'PASSWORD=ignoredpassword\n' | se_cli set HOSTNAME=calm --json
  assert_eq "$(jq -c '.changed' "$TEST_TMP/out")" '["HOSTNAME"]'
}

test_config_network_change_snapshots_and_arms_the_backstop() {
  se_cli_env
  printf 'WIFI_PASSWORD=newwifipass\n' | se_cli set WIFI_SSID=Other --stdin --json
  assert_eq "$?" "0"
  assert_eq "$(jq -r '.network' "$TEST_TMP/out")" "true"
  assert_ok dxb_netsafe_pending
  assert_contains "$(cat "$TEST_TMP/calls")" "--on-active=600"
  # the snapshot holds the file as it was before the change
  assert_contains "$(cat "$DXB_NETSAFE_DIR"/*)" "WIFI_SSID=Shack Net"
  # a second change waits until this one is kept or undone
  se_cli set STATIC_IP=10.0.0.91/24; assert_eq "$?" "5"
  assert_contains "$(cat "$TEST_TMP/err")" "kept or undone"
}

test_config_set_refuses_while_a_job_runs() {
  se_cli_env
  assert_ok se_cli set HOSTNAME=busy --json
  jq -r '.job' "$TEST_TMP/out" > "$TEST_TMP/active"
  se_cli set TIMEZONE=UTC; assert_eq "$?" "5"
  assert_contains "$(cat "$TEST_TMP/err")" "still being applied"
}

test_config_set_rolls_back_when_the_job_cannot_start() {
  local before
  se_cli_env
  before=$(se_file)
  touch "$TEST_TMP/systemd-run-fails"
  se_cli set HOSTNAME=never --json; assert_eq "$?" "6"
  assert_eq "$(se_file)" "$before"
  # the rollback must put dxberry.txt back WITHOUT overwriting .prev with the change that was
  # never applied: .prev still holds what it held before this whole (failed) attempt
  assert_eq "$(cat "$DXB_CONFIG_PREV")" "$before"
  [[ -e $DXB_CONFIG_PENDING ]] && _fail "a rolled-back change must not leave a pending-keys file"
  se_cli set WIFI_SSID=Never --json; assert_eq "$?" "6"
  assert_eq "$(se_file)" "$before"
  assert_fails dxb_netsafe_pending
  [[ -e $DXB_CONFIG_PENDING ]] && _fail "a rolled-back network change must not leave a pending-keys file"
}

# A network change whose undo timer arms fine but whose job never starts (a different failure
# point than the timer itself failing to arm) must still roll back the file, drop the snapshot,
# stop the timer it just armed, and forget its deadline.
test_config_set_rolls_back_a_network_change_when_only_the_job_fails_to_start() {
  local before
  se_cli_env
  before=$(se_file)
  touch "$TEST_TMP/job-start-fails"
  se_cli set WIFI_SSID=Never --json; assert_eq "$?" "6"
  assert_eq "$(se_file)" "$before"
  assert_fails dxb_netsafe_pending
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl stop dxberry-config-revert-"
  [[ -e $DXB_NETSAFE_AT ]] && _fail "the re-armed deadline file must be removed when the job never started"
  [[ -e $DXB_CONFIG_PENDING ]] && _fail "the pending keys file must be removed when the job never started"
}

test_config_apply_keeps_graywolf_rearms_and_records_its_exit() {
  se_cli_env
  printf 'WIFI_SSID\n' > "$DXB_CONFIG_PENDING"
  dxb_netsafe_snapshot
  assert_ok se_cli apply
  assert_contains "$(cat "$TEST_TMP/calls")" "provision DXB_GW_UPGRADE=0"
  assert_contains "$(cat "$TEST_TMP/calls")" "--on-active=120"
  assert_eq "$(jq -r '.exit' "$DXB_CONFIG_RESULT")" "0"
  [[ -e $DXB_CONFIG_PENDING ]] && _fail "apply must consume the pending keys"
  echo 1 > "$TEST_TMP/provision-rc"; printf 'HOSTNAME\n' > "$DXB_CONFIG_PENDING"; : > "$TEST_TMP/calls"
  se_cli apply; assert_eq "$?" "8"
  assert_eq "$(jq -r '.exit' "$DXB_CONFIG_RESULT")" "8"
  assert_not_contains "$(cat "$TEST_TMP/calls")" "on-active"
}

# A network key with no snapshot waiting (confirmed or already undone before apply ran) must never
# re-arm the backstop - and, since the guard short-circuits first, never even try the lock.
test_config_apply_skips_the_rearm_without_a_pending_snapshot() {
  se_cli_env
  printf 'WIFI_SSID\n' > "$DXB_CONFIG_PENDING"
  assert_ok se_cli apply
  assert_not_contains "$(cat "$TEST_TMP/calls")" "--on-active=120"
}

# The provisioner is a CHILD PROCESS: main()'s own export DXB_LOG_FILE=/dev/null must never reach
# it (a real provisioner run under that export would "chmod 600 /dev/null" as root, and every
# other program on the box would then fail to open /dev/null until a reboot). It also needs its
# own umask (022, what it is written for - e.g. its /etc/hosts fallback rewrite), not this
# command's 077.
test_config_apply_runs_the_provisioner_with_its_own_log_and_umask() {
  se_cli_env
  cat > "$TEST_TMP/provision-stub" << 'STUB'
#!/bin/bash
echo "LOG=$DXB_LOG_FILE UPGRADE=$DXB_GW_UPGRADE UMASK=$(umask)" >> "$STUB_OUT"
STUB
  chmod +x "$TEST_TMP/provision-stub"
  export DXB_PROVISION_CMD="$TEST_TMP/provision-stub" STUB_OUT="$TEST_TMP/stub-out"
  unset DXB_LOG_FILE
  assert_ok se_cli apply
  assert_contains "$(cat "$TEST_TMP/stub-out")" "LOG=$DXB_STATE_DIR/provision.log UPGRADE=0 UMASK=0022"
}

test_config_apply_pushes_position_log_and_gps_to_graywolf() {
  se_cli_env
  printf 'POSITION_LOG\n' > "$DXB_CONFIG_PENDING"
  (
    source "$DXB_ROOT/provision/bin/dxberry-config"
    dxb_require_root() { :; }
    systemctl() { :; }
    dxb_gw_wait_ready() { return 0; }
    dxb_gw_login_any() { echo login >> "$TEST_TMP/calls"; }
    dxb_gw_seed_position_log() { echo "seed position_log ${DXB_CFG[POSITION_LOG]}" >> "$TEST_TMP/calls"; }
    dxb_gw_seed_gps() { echo "seed gps" >> "$TEST_TMP/calls"; }
    dxb_gw_api() { echo "api $*" >> "$TEST_TMP/calls"; }
    main apply
  ) > /dev/null 2>&1
  assert_contains "$(cat "$TEST_TMP/calls")" "seed position_log on"
  assert_contains "$(cat "$TEST_TMP/calls")" "seed gps"
  assert_contains "$(cat "$TEST_TMP/calls")" "api POST /auth/logout"
}

test_config_job_reports_state_lines_and_the_undo() {
  se_cli_env
  se_cli job --json
  assert_eq "$(jq -c '{job, pending, revert_at, reverted_at, reboot_required}' "$TEST_TMP/out")" \
    '{"job":null,"pending":false,"revert_at":null,"reverted_at":null,"reboot_required":false}'
  printf 'WIFI_PASSWORD=newwifipass\n' | se_cli set WIFI_SSID=Other --stdin --json
  jq -r '.job' "$TEST_TMP/out" > "$TEST_TMP/active"
  se_cli job --json
  assert_eq "$(jq -r '.job.state, .job.network, .pending' "$TEST_TMP/out" | tr '\n' ' ')" "running true true "
  assert_eq "$(jq -c '.job.lines' "$TEST_TMP/out")" '["applying: HOSTNAME","finished: all steps completed"]'
  [[ $(jq -r '.revert_at' "$TEST_TMP/out") =~ ^[0-9]+$ ]] || _fail "revert_at must be an epoch while the undo is armed"
  : > "$TEST_TMP/active"
  echo '{"exit":8,"finished":1700000300}' > "$DXB_CONFIG_RESULT"
  touch "$DXB_REBOOT_FLAG"
  se_cli job --json
  assert_eq "$(jq -r '.job.state, .job.exit, .reboot_required' "$TEST_TMP/out" | tr '\n' ' ')" "finished 8 true "
  rm -f "$DXB_CONFIG_RESULT"
  se_cli job --json
  assert_eq "$(jq -r '.job.state' "$TEST_TMP/out")" "ended"
}

test_config_confirm_and_revert() {
  se_cli_env
  printf 'WIFI_PASSWORD=newwifipass\n' | se_cli set WIFI_SSID=Other --stdin --json
  assert_ok se_cli confirm --json
  assert_eq "$(se_out)" '{"ok":true,"kept":true}'
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl stop dxberry-config-revert-"
  assert_fails dxb_netsafe_pending
  assert_file_contains "$DXB_BOOT_DIR/dxberry.txt" "WIFI_SSID=Other"
  # nothing waiting (the undo may have already won the race): say so, never "kept"
  assert_ok se_cli confirm --json
  assert_eq "$(se_out)" '{"ok":true,"kept":false}'
  # revert: the file comes back, netwatch restarts, the undo is recorded
  se_cli set WIFI_SSID=Again --json
  assert_ok se_cli revert --json
  assert_file_contains "$DXB_BOOT_DIR/dxberry.txt" "WIFI_SSID=Other"
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl restart dxberry-netwatch"
  [[ -s $DXB_CONFIG_REVERTED ]] || _fail "revert must record when it ran"
  se_cli revert; assert_eq "$?" "6"
  # at boot: files only, no netwatch restart (it has not started yet)
  se_cli set WIFI_SSID=Boot --json; : > "$TEST_TMP/calls"
  assert_ok se_cli revert --boot
  assert_not_contains "$(cat "$TEST_TMP/calls")" "restart dxberry-netwatch"
  assert_file_contains "$DXB_BOOT_DIR/dxberry.txt" "WIFI_SSID=Other"
}

# A restore that fails (one file could not be put back) must never be recorded as reverted - the
# page would otherwise show both "keep?" and "was undone" for the same change - and nothing else
# would ever retry it. Guarded like test_settings_write_keeps_the_previous_file_private: root can
# write through the permissions this test breaks, so the scenario cannot be forced that way as root.
test_config_revert_retries_when_the_restore_fails() {
  se_cli_env
  printf 'WIFI_PASSWORD=newwifipass\n' | se_cli set WIFI_SSID=Other --stdin --json
  assert_eq "$?" "0"
  if (( EUID != 0 )); then
    chmod 500 "$TEST_TMP/etc"
    se_cli revert; assert_eq "$?" "6"
    assert_contains "$(cat "$TEST_TMP/err")" "trying again"
    assert_ok dxb_netsafe_pending
    [[ -s $DXB_CONFIG_REVERTED ]] && _fail "a failed restore must not be recorded as reverted"
    assert_contains "$(cat "$TEST_TMP/calls")" "--on-active=60"
    chmod 700 "$TEST_TMP/etc"
  fi
}

test_config_usage() {
  se_cli_env
  se_cli; assert_eq "$?" "2"
  se_cli frobnicate; assert_eq "$?" "2"
  assert_ok se_cli -h
  assert_contains "$(se_out)" "set KEY=VALUE"
}
