#!/usr/bin/env bash
# Callbacks and variables stay in fixture subshells; network files use TEST_TMP.
# shellcheck disable=SC1090,SC1091,SC2030,SC2031,SC2034,SC2317

settings_execution_wait_file() {
  local deadline=$((SECONDS + 5))
  while [[ ! -e $1 ]]; do
    (( SECONDS < deadline )) || { _fail "timed out waiting for $1"; return 1; }
    command sleep 0.01
  done
}

test_settings_execution_lock_preserves_pending_and_result_on_contention() (
  se_cli_env
  echo HOSTNAME > "$DXB_CONFIG_PENDING"
  echo '{"exit":47,"finished":123}' > "$DXB_CONFIG_RESULT"
  (umask 077; : > "$DXB_RUN_DIR/config-apply.lock")
  local holder
  exec {holder}< "$DXB_RUN_DIR/config-apply.lock"
  command flock -s "$holder"
  DXB_CONFIG_APPLY_LOCK_WAIT=0 se_cli apply
  assert_eq "$?" 6
  assert_eq "$(cat "$DXB_CONFIG_PENDING")" HOSTNAME
  assert_eq "$(cat "$DXB_CONFIG_RESULT")" '{"exit":47,"finished":123}'
  assert_not_contains "$(cat "$TEST_TMP/calls")" 'provision DXB_GW_UPGRADE'
  exec {holder}>&-
  assert_ok se_cli apply
  assert_eq "$(jq -r .exit "$DXB_CONFIG_RESULT")" 0
  [[ ! -e $DXB_CONFIG_PENDING ]] || _fail 'successful retry retained pending keys'
  assert_ok command flock -s -n "$DXB_RUN_DIR/config-apply.lock" true
)

test_settings_execution_lock_covers_provision_push_and_result() (
  local base=$TEST_TMP phase worker
  for phase in provision push result; do
    TEST_TMP=$base/$phase; export TEST_TMP
    se_cli_env
    if [[ $phase == push ]]; then echo POSITION_LOG; else echo HOSTNAME; fi > "$DXB_CONFIG_PENDING"
    (
      source "$DXB_ROOT/provision/bin/dxberry-config"
      dxb_require_root() { :; }
      systemctl() { :; }
      settings_execution_pause() { : > "$TEST_TMP/entered"; settings_execution_wait_file "$TEST_TMP/release"; }
      se_provision() { [[ $phase != provision ]] || settings_execution_pause; }
      dxb_settings_push_graywolf() { settings_execution_pause; }
      jq() {
        if [[ $phase == result && $1 == -cn && ${2:-} == --argjson && ${3:-} == e ]]; then settings_execution_pause || return 72; fi
        command jq "$@"
      }
      main apply
    ) > "$TEST_TMP/worker-out" 2> "$TEST_TMP/worker-err" &
    worker=$!
    trap 'touch "$TEST_TMP/release"; kill "$worker" 2>/dev/null; wait "$worker" 2>/dev/null || true' EXIT
    settings_execution_wait_file "$TEST_TMP/entered" || return
    if command flock -s -n "$DXB_RUN_DIR/config-apply.lock" true; then _fail "$phase escaped execution lock"; fi
    assert_ok command flock -s -n "$DXB_RUN_DIR/config.lock" true
    assert_ok se_cli job --json
    assert_eq "$(jq -r .job "$TEST_TMP/out")" null
    : > "$TEST_TMP/release"
    wait "$worker"; assert_eq "$?" 0
    assert_eq "$(jq -r .exit "$DXB_CONFIG_RESULT")" 0
    assert_ok command flock -s -n "$DXB_RUN_DIR/config-apply.lock" true
  done
)

test_settings_execution_lock_leaves_confirm_and_revert_available() (
  local base=$TEST_TMP recovery worker
  for recovery in confirm revert; do
    TEST_TMP=$base/$recovery; export TEST_TMP
    se_cli_env
    echo WIFI_SSID > "$DXB_CONFIG_PENDING"
    dxb_netsafe_snapshot
    (
      source "$DXB_ROOT/provision/bin/dxberry-config"
      dxb_require_root() { :; }
      systemctl() { :; }
      se_provision() { : > "$TEST_TMP/entered"; settings_execution_wait_file "$TEST_TMP/release"; }
      main apply
    ) > "$TEST_TMP/worker-out" 2> "$TEST_TMP/worker-err" &
    worker=$!
    trap 'touch "$TEST_TMP/release"; kill "$worker" 2>/dev/null; wait "$worker" 2>/dev/null || true' EXIT
    settings_execution_wait_file "$TEST_TMP/entered" || return
    assert_ok se_cli "$recovery" --boot --json
    assert_fails dxb_netsafe_pending
    : > "$TEST_TMP/release"
    wait "$worker"; assert_eq "$?" 0
    assert_not_contains "$(cat "$TEST_TMP/calls")" '--on-active=120'
    assert_eq "$(jq -r .exit "$DXB_CONFIG_RESULT")" 0
  done
)

test_settings_execution_lock_protects_exit_cleanup_and_closes() (
  se_cli_env
  echo POSITION_LOG > "$DXB_CONFIG_PENDING"
  (umask 077; : > "$DXB_RUN_DIR/config-apply.lock")
  (
    source "$DXB_ROOT/provision/bin/dxberry-config"
    dxb_require_root() { :; }
    se_provision() { :; }
    dxb_settings_push_graywolf() {
      trap 'if command flock -s -n "$DXB_RUN_DIR/config-apply.lock" true; then : > "$TEST_TMP/escaped"; else : > "$TEST_TMP/protected"; fi' EXIT
      exit 73
    }
    main apply
    echo "$?" > "$TEST_TMP/returned"
  ) > /dev/null 2>&1
  [[ -e $TEST_TMP/returned ]] || _fail 'body exit escaped command subshell'
  assert_eq "$(cat "$TEST_TMP/returned" 2>/dev/null)" 73
  [[ -e $TEST_TMP/protected && ! -e $TEST_TMP/escaped ]] || _fail 'exit cleanup escaped execution lock'
  assert_ok command flock -s -n "$DXB_RUN_DIR/config-apply.lock" true
)

test_settings_execution_lock_refuses_unsafe_or_unavailable_state() (
  local base=$TEST_TMP fault
  for fault in missing-flock failed-flock bad-directory symlink-directory symlink fifo hardlink mode content replaced-file replaced-directory; do
    TEST_TMP=$base/$fault; export TEST_TMP
    se_cli_env
    local lock=$DXB_RUN_DIR/config-apply.lock
    case $fault in
      bad-directory) mv "$DXB_RUN_DIR" "$DXB_RUN_DIR.real"; echo retained > "$DXB_RUN_DIR" ;;
      symlink-directory) mv "$DXB_RUN_DIR" "$DXB_RUN_DIR.real"; ln -s "$DXB_RUN_DIR.real" "$DXB_RUN_DIR" ;;
      symlink) echo retained > "$TEST_TMP/target"; ln -s "$TEST_TMP/target" "$lock" ;;
      fifo) mkfifo "$lock" ;;
      hardlink) (umask 077; : > "$lock"); ln "$lock" "$TEST_TMP/other" ;;
      mode) : > "$lock"; chmod 644 "$lock" ;;
      content) (umask 077; echo retained > "$lock") ;;
    esac
    (
      source "$DXB_ROOT/provision/bin/dxberry-config"
      dxb_require_root() { :; }
      se_provision() { : > "$TEST_TMP/live-effect"; return 0; }
      case $fault in
        missing-flock) command() { [[ $* != '-v flock' ]] && builtin command "$@"; } ;;
        failed-flock) flock() { return 1; } ;;
        replaced-file) flock() { mv "$lock" "$lock.old"; (umask 077; : > "$lock"); } ;;
        replaced-directory) flock() { mv "$DXB_RUN_DIR" "$DXB_RUN_DIR.old"; mkdir "$DXB_RUN_DIR"; } ;;
      esac
      main apply
    ) > "$TEST_TMP/out" 2> "$TEST_TMP/err"
    assert_eq "$?" 6
    [[ ! -e $TEST_TMP/live-effect ]] || _fail "$fault entered settings work"
    if [[ $fault == symlink ]]; then assert_eq "$(cat "$TEST_TMP/target")" retained; fi
  done
)
