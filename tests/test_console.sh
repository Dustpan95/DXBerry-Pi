#!/usr/bin/env bash
# shellcheck disable=SC1091,SC2034,SC2317
source "$DXB_LIB/common.sh"
source "$DXB_LIB/config.sh"
source "$DXB_LIB/console.sh"
source "$DXB_LIB/settings.sh"

co_env() {
  export DXB_STATE_DIR=$TEST_TMP/state DXB_LOG_FILE=$TEST_TMP/state/log DXB_TEMPLATES=$DXB_ROOT/provision/templates \
    DXB_COCKPIT_SRC=$TEST_TMP/opt/cockpit/dxberry DXB_COCKPIT_LINK=$TEST_TMP/usr-share-cockpit/dxberry \
    DXB_COCKPIT_DROPIN=$TEST_TMP/systemd/cockpit.socket.d/dxberry-listen.conf \
    DXB_NETSAFE_UNIT_FILE=$TEST_TMP/units/dxberry-config-boot.service
  mkdir -p "$DXB_STATE_DIR" "$DXB_COCKPIT_SRC"
  : > "$TEST_TMP/calls"
  DXB_STATUS_LINES=(); DXB_FAILED_STEPS=()
  DXB_CFG=([CONSOLE]=on [_IP]=10.0.0.90)
}
# co_run CMD...: CMD with fake systemctl/apt-get/dpkg-query in a subshell. The step's failed steps and
# status lines come back through $TEST_TMP/failed and $TEST_TMP/status (the subshell's arrays die with it).
co_run() {
  (
    systemctl() {
      echo "systemctl $*" >> "$TEST_TMP/calls"
      case $1 in
        is-enabled) if [[ -f $TEST_TMP/logind-masked ]]; then echo masked; return 1; fi; echo static ;;
        restart) [[ -f $TEST_TMP/restart-fails ]] && return 1 ;;
        show)
          if [[ $2 == -p && $3 == Listen && $4 == cockpit.socket ]]; then
            if [[ -f $TEST_TMP/cockpit-socket-9090 ]]; then
              printf 'Listen=[::]:9090 (Stream)\n'
            else
              printf 'Listen=[::]:443 (Stream)\nListen=[::]:80 (Stream)\n'
            fi
          fi ;;
      esac
      return 0
    }
    apt-get() { echo "apt-get $*" >> "$TEST_TMP/calls"; [[ -f $TEST_TMP/apt-fails ]] && return 100; : > "$TEST_TMP/cockpit-installed"; }
    dpkg-query() { [[ -f $TEST_TMP/cockpit-installed ]] || return 1; printf installed; }
    "$@"
    printf '%s\n' "${DXB_FAILED_STEPS[@]}" > "$TEST_TMP/failed"
    printf '%s\n' "${DXB_STATUS_LINES[@]}" > "$TEST_TMP/status"
  ) 2> /dev/null
}
co_calls() { cat "$TEST_TMP/calls"; }

test_console_installs_cockpit_without_recommends() {
  co_env
  co_run provision_console
  assert_contains "$(co_calls)" "apt-get install -y --no-install-recommends cockpit-ws cockpit-bridge cockpit-system"
  assert_eq "$(cat "$TEST_TMP/failed")" ""
}

test_console_skips_the_install_when_cockpit_is_there() {
  co_env; : > "$TEST_TMP/cockpit-installed"
  co_run provision_console
  assert_not_contains "$(co_calls)" "apt-get"
}

test_console_install_failure_is_a_failed_step_and_stops_there() {
  co_env; : > "$TEST_TMP/apt-fails"
  co_run provision_console
  assert_contains "$(cat "$TEST_TMP/failed")" "console: could not install cockpit-ws cockpit-bridge cockpit-system"
  assert_eq "$(cat "$TEST_TMP/status")" "console: unavailable (Cockpit is not installed)"
  [[ -e $DXB_COCKPIT_DROPIN ]] && _fail "no listen drop-in without Cockpit"
  assert_not_contains "$(co_calls)" "cockpit.socket"
}

test_console_unmasks_logind_only_when_masked_and_always_starts_it() {
  co_env; : > "$TEST_TMP/cockpit-installed"; : > "$TEST_TMP/logind-masked"
  co_run provision_console
  assert_contains "$(co_calls)" "systemctl unmask systemd-logind"
  assert_contains "$(co_calls)" "systemctl start systemd-logind"
  rm -f "$TEST_TMP/logind-masked"; : > "$TEST_TMP/calls"
  co_run provision_console
  assert_not_contains "$(co_calls)" "unmask"
  assert_contains "$(co_calls)" "systemctl start systemd-logind"
}

test_console_listens_on_443_and_80_and_restarts_only_on_change() {
  co_env; : > "$TEST_TMP/cockpit-installed"
  co_run provision_console
  assert_eq "$(grep '^ListenStream=' "$DXB_COCKPIT_DROPIN" | tr '\n' ' ')" "ListenStream= ListenStream=443 ListenStream=80 "
  assert_eq "$(readlink "$DXB_COCKPIT_LINK")" "$DXB_COCKPIT_SRC"
  assert_contains "$(co_calls)" "systemctl daemon-reload"
  assert_contains "$(co_calls)" "systemctl restart cockpit.socket"
  assert_contains "$(co_calls)" "systemctl enable --now cockpit.socket"
  : > "$TEST_TMP/calls"
  co_run provision_console
  assert_not_contains "$(co_calls)" "restart cockpit.socket"
  assert_not_contains "$(co_calls)" "daemon-reload"
  assert_contains "$(co_calls)" "systemctl enable --now cockpit.socket"
  assert_eq "$(cat "$TEST_TMP/failed")" ""
}

test_console_status_line_names_the_address() {
  co_env; : > "$TEST_TMP/cockpit-installed"
  co_run provision_console
  assert_eq "$(cat "$TEST_TMP/status")" "console: https://10.0.0.90/ (log in as dietpi)"
  DXB_CFG=([CONSOLE]=on)
  co_run provision_console
  assert_eq "$(cat "$TEST_TMP/status")" "console: https://<this-pi>/ (log in as dietpi)"
}

test_console_off_stops_cockpit_and_removes_the_page() {
  co_env; : > "$TEST_TMP/cockpit-installed"
  co_run provision_console
  DXB_CFG[CONSOLE]=off; : > "$TEST_TMP/calls"
  co_run provision_console
  assert_contains "$(co_calls)" "systemctl disable --now cockpit.socket"
  [[ -e $DXB_COCKPIT_LINK || -L $DXB_COCKPIT_LINK ]] && _fail "the page link must be gone with CONSOLE=off"
  assert_eq "$(cat "$TEST_TMP/status")" "console: off (CONSOLE=off)"
  assert_not_contains "$(co_calls)" "apt-get"
  assert_not_contains "$(co_calls)" "logind"
}

test_console_never_replaces_a_directory_it_did_not_make() {
  co_env; : > "$TEST_TMP/cockpit-installed"
  mkdir -p "$DXB_COCKPIT_LINK"; : > "$DXB_COCKPIT_LINK/keep"
  co_run provision_console
  assert_contains "$(cat "$TEST_TMP/failed")" "is not DXBerry's link; left alone"
  [[ -f $DXB_COCKPIT_LINK/keep ]] || _fail "a foreign directory was touched"
}

test_console_restart_failure_is_a_failed_step() {
  co_env; : > "$TEST_TMP/cockpit-installed"; : > "$TEST_TMP/restart-fails"
  co_run provision_console
  assert_contains "$(cat "$TEST_TMP/failed")" "console: could not restart cockpit.socket on ports 443 and 80"
  assert_eq "$(cat "$TEST_TMP/status")" "console: unavailable (see FAILED STEPS)"
}

# An unchanged drop-in is not proof the restart it once needed actually happened: if the socket
# somehow ended up back on 9090 (an earlier restart failed, say), the next run must still retry.
test_console_retries_the_restart_when_unchanged_but_not_listening_on_443() {
  co_env; : > "$TEST_TMP/cockpit-installed"
  co_run provision_console
  : > "$TEST_TMP/cockpit-socket-9090"
  : > "$TEST_TMP/calls"
  co_run provision_console
  assert_contains "$(co_calls)" "systemctl daemon-reload"
  assert_contains "$(co_calls)" "systemctl restart cockpit.socket"
  assert_eq "$(cat "$TEST_TMP/status")" "console: https://10.0.0.90/ (log in as dietpi)"
}

# A listen drop-in that could not be written (a read-only /etc, say) must never report the
# console as reachable.
test_console_status_line_names_a_failed_listen_write() {
  co_env; : > "$TEST_TMP/cockpit-installed"
  mkdir -p "$TEST_TMP/systemd"
  : > "$TEST_TMP/systemd/cockpit.socket.d"   # a file where dxb_console_listen needs a directory
  co_run provision_console
  assert_contains "$(cat "$TEST_TMP/failed")" "could not write"
  assert_eq "$(cat "$TEST_TMP/status")" "console: unavailable (see FAILED STEPS)"
}

test_console_listen_template_replaces_9090() {
  local t=$DXB_ROOT/provision/templates/cockpit-listen.conf
  assert_eq "$(grep -m1 '^ListenStream' "$t")" "ListenStream="
  assert_file_not_contains "$t" "9090"
}

# The boot unit that undoes an unkept network change is installed whatever CONSOLE says.
test_console_installs_the_network_undo_boot_unit_even_when_off() {
  co_env
  co_run provision_console
  assert_file_contains "$DXB_NETSAFE_UNIT_FILE" "ExecStart=/opt/dxberry/bin/dxberry-config revert --boot"
  assert_file_contains "$DXB_NETSAFE_UNIT_FILE" "Before=dxberry-netwatch.service"
  assert_contains "$(co_calls)" "systemctl enable dxberry-config-boot.service"
  assert_eq "$(cat "$TEST_TMP/failed")" ""
  rm -f "$DXB_NETSAFE_UNIT_FILE"
  DXB_CFG[CONSOLE]=off
  co_run provision_console
  assert_file_contains "$DXB_NETSAFE_UNIT_FILE" "dxberry-config revert --boot"
  assert_eq "$(cat "$TEST_TMP/status")" "console: off (CONSOLE=off)"
}
