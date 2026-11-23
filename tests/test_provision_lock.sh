#!/usr/bin/env bash
# Each scenario isolates fixture variables in a subshell and observes files.
# shellcheck disable=SC1090,SC1091,SC2030,SC2031,SC2034,SC2317

provision_lock_wait_file() {
  local until=$((SECONDS + 5))
  while [[ ! -e $1 ]]; do
    (( SECONDS < until )) || { _fail "timed out waiting for $1"; return 1; }
    command sleep 0.01
  done
}

test_provision_operation_lock_covers_live_work_and_recovers() (
  provision_tracking_fixture
  mkdir -p "$DXB_RUN_DIR"
  ( umask 077; : > "$DXB_RUN_DIR/provision.lock" )
  (
    source "$DXB_ROOT/provision/bin/dxberry-provision"
    dxb_require_root() { :; }
    provision_system() {
      : > "$TEST_TMP/entered"
      provision_lock_wait_file "$TEST_TMP/release" || exit 72
      exit 73
    }
    main
  ) > "$TEST_TMP/first-out" 2> "$TEST_TMP/first-err" &
  local worker=$!
  trap 'touch "$TEST_TMP/release"; kill "$worker" 2>/dev/null; wait "$worker" 2>/dev/null || true' EXIT
  provision_lock_wait_file "$TEST_TMP/entered" || return
  if command flock -s -n "$DXB_RUN_DIR/provision.lock" true; then _fail 'reader entered during full provisioning work'; fi
  (
    source "$DXB_ROOT/provision/bin/dxberry-provision"
    dxb_require_root() { :; }
    provision_system() { : > "$TEST_TMP/second-effect"; exit 74; }
    DXB_PROVISION_LOCK_WAIT=0 main
  ) > "$TEST_TMP/second-out" 2> "$TEST_TMP/second-err"
  assert_eq "$?" 6
  [[ ! -e $TEST_TMP/second-effect ]] || _fail 'contending provisioner entered live work'
  assert_eq "$(wc -l < "$TEST_TMP/tracking-calls")" 1
  (
    source "$DXB_ROOT/provision/bin/dxberry-provision"
    main --check > /dev/null
  )
  assert_eq "$?" 0
  : > "$TEST_TMP/release"
  wait "$worker"
  assert_eq "$?" 73
  assert_ok command flock -s -n "$DXB_RUN_DIR/provision.lock" true
  (
    source "$DXB_ROOT/provision/bin/dxberry-provision"
    dxb_require_root() { :; }
    provision_system() { : > "$TEST_TMP/retry-effect"; exit 74; }
    DXB_PROVISION_LOCK_WAIT=0 main
  ) > /dev/null 2>&1
  assert_eq "$?" 74
  [[ -e $TEST_TMP/retry-effect ]] || _fail 'released lock did not allow retry'
  assert_eq "$(wc -l < "$TEST_TMP/tracking-calls")" 2
)

test_provision_operation_lock_refuses_unsafe_or_unavailable_state() (
  local base=$TEST_TMP fault
  for fault in missing-flock failed-flock bad-directory symlink-directory symlink fifo hardlink mode content replaced-file replaced-directory; do
    TEST_TMP=$base/$fault; export TEST_TMP
    provision_tracking_fixture
    mkdir -p "$DXB_RUN_DIR"
    local lock=$DXB_RUN_DIR/provision.lock
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
      source "$DXB_ROOT/provision/bin/dxberry-provision"
      dxb_require_root() { :; }
      provision_system() { : > "$TEST_TMP/live-effect"; exit 73; }
      case $fault in
        missing-flock) command() { [[ $* != '-v flock' ]] && builtin command "$@"; } ;;
        failed-flock) flock() { return 1; } ;;
        replaced-file) flock() { mv "$lock" "$lock.old"; (umask 077; : > "$lock"); } ;;
        replaced-directory) flock() { mv "$DXB_RUN_DIR" "$DXB_RUN_DIR.old"; mkdir "$DXB_RUN_DIR"; } ;;
      esac
      main
    ) > "$TEST_TMP/out" 2> "$TEST_TMP/err"
    assert_eq "$?" 6
    [[ ! -e $TEST_TMP/live-effect ]] || _fail "$fault allowed provisioning work"
    [[ ! -e $TEST_TMP/tracking-calls ]] || _fail "$fault advanced before locking"
    [[ ! -e $DXB_STATE_DIR/status.txt ]] || _fail "$fault published completion"
    [[ $fault != symlink ]] || assert_eq "$(cat "$TEST_TMP/target")" retained
  done
)

test_provision_operation_lock_closes_on_return_and_noops_create_nothing() (
  provision_tracking_fixture
  source "$DXB_ROOT/provision/bin/dxberry-provision"
  dxb_require_root() { :; }
  main --check > /dev/null
  assert_eq "$?" 0
  main --help > /dev/null
  assert_eq "$?" 0
  mkdir -p "$DXB_STATE_DIR"
  : > "$DXB_STATE_DIR/provisioned"
  main --first-boot > /dev/null
  assert_eq "$?" 0
  [[ ! -e $DXB_RUN_DIR/provision.lock ]] || _fail 'no-op acquired a provisioning lock'
  dxb_program_generation_prepare_advance() { return 6; }
  main > /dev/null 2>&1
  assert_eq "$?" 6
  [[ -f $DXB_RUN_DIR/provision.lock ]] || _fail 'full run did not initialize its lock'
  assert_ok command flock -s -n "$DXB_RUN_DIR/provision.lock" true
)

test_provision_operation_lock_rechecks_completed_first_boot_after_wait() (
  provision_tracking_fixture
  (
    source "$DXB_ROOT/provision/bin/dxberry-provision"
    dxb_require_root() { :; }
    provision_system() {
      : > "$TEST_TMP/entered"
      provision_lock_wait_file "$TEST_TMP/release" || exit 72
      exit 73
    }
    main --first-boot
  ) > /dev/null 2>&1 &
  local first=$! second=''
  trap 'touch "$TEST_TMP/release"; kill "$first" ${second:+"$second"} 2>/dev/null; wait "$first" ${second:+"$second"} 2>/dev/null || true' EXIT
  provision_lock_wait_file "$TEST_TMP/entered" || return
  (
    source "$DXB_ROOT/provision/bin/dxberry-provision"
    dxb_require_root() { :; }
    provision_system() { : > "$TEST_TMP/duplicate-effect"; exit 74; }
    flock() { : > "$TEST_TMP/waiting"; command flock "$@"; }
    main --first-boot
  ) > /dev/null 2>&1 &
  second=$!
  provision_lock_wait_file "$TEST_TMP/waiting" || return
  : > "$DXB_STATE_DIR/provisioned"
  : > "$TEST_TMP/release"
  wait "$first"; assert_eq "$?" 73
  wait "$second"; assert_eq "$?" 0
  [[ ! -e $TEST_TMP/duplicate-effect ]] || _fail 'waiter repeated completed first-boot work'
  assert_eq "$(wc -l < "$TEST_TMP/tracking-calls")" 1
)
