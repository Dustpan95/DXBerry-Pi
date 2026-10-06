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
se_systemd_run() { echo "systemd-run $*" >> "$TEST_TMP/calls"; [[ ! -f $TEST_TMP/systemd-run-fails ]]; }

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
