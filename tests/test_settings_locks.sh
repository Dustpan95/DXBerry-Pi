#!/usr/bin/env bash
# Independently isolated test subshells intentionally set their own environment.
# shellcheck disable=SC1090,SC1091,SC2030,SC2031,SC2034,SC2317

settings_lock_refusal_fixture() (
  local entry=$1 fault=$2 fd=7
  export DXB_STATE_DIR=$TEST_TMP/state DXB_RUN_DIR=$TEST_TMP/run
  export DXB_LOG_FILE=/dev/null
  mkdir -p "$DXB_STATE_DIR" "$DXB_RUN_DIR"
  exec 6>&- 7>&-
  case $entry in
    update)
      source "$DXB_ROOT/provision/bin/dxberry-update"
      fd=6
      cmd_start() { : > "$TEST_TMP/unguarded"; }
      ;;
    job)
      source "$DXB_LIB/common.sh"
      source "$DXB_LIB/update.sh"
      dxb_update_job_running() { : > "$TEST_TMP/unguarded"; return 0; }
      ;;
    set|confirm|revert)
      source "$DXB_ROOT/provision/bin/dxberry-config"
      cmd_set() { : > "$TEST_TMP/unguarded"; }
      cmd_confirm() { : > "$TEST_TMP/unguarded"; }
      cmd_revert() { : > "$TEST_TMP/unguarded"; }
      ;;
  esac
  dxb_require_root() { :; }
  local lock=$DXB_RUN_DIR/config.lock
  [[ $entry == update ]] && lock=$DXB_RUN_DIR/update.lock
  case $fault in
    missing)
      command() {
        [[ ${1:-} == -v && ${2:-} == flock ]] && return 1
        builtin command "$@"
      }
      ;;
    open) mkdir "$lock" ;;
    directory) rmdir "$DXB_RUN_DIR"; : > "$DXB_RUN_DIR" ;;
    busy) flock() { return 73; } ;;
  esac
  case $entry in
    update) main system ;;
    job) dxb_update_job_start system ;;
    *) main "$entry" ;;
  esac > "$TEST_TMP/out" 2> "$TEST_TMP/err"
  assert_eq "$?" 5
  [[ ! -e $TEST_TMP/unguarded ]] || _fail "$entry proceeded without its $fault lock"
  if [[ $fault == busy ]]; then
    assert_contains "$(cat "$TEST_TMP/err")" 'try again'
  else
    assert_contains "$(cat "$TEST_TMP/err")" lock
  fi
  if { : >&"$fd"; } 2>/dev/null; then _fail "$entry leaked lock descriptor $fd after $fault"; fi
)

test_settings_and_update_mutations_require_their_locks() (
  local entry fault
  for entry in update job set confirm revert; do
    for fault in missing open directory busy; do
      settings_lock_refusal_fixture "$entry" "$fault"
      rm -rf "$TEST_TMP/run" "$TEST_TMP/unguarded"
    done
  done
)

test_settings_and_update_locks_refuse_real_contention_then_recover() (
  export DXB_RUN_DIR=$TEST_TMP/run DXB_UPDATE_LOCK_WAIT=0 DXB_CONFIG_LOCK_WAIT=0
  mkdir -p "$DXB_RUN_DIR"
  source "$DXB_ROOT/provision/bin/dxberry-config"
  local helper fd file holder
  for helper in dxb_update_lock dxb_update_lock_settings lock_config; do
    fd=7 file=$DXB_RUN_DIR/config.lock
    [[ $helper == dxb_update_lock ]] && { fd=6; file=$DXB_RUN_DIR/update.lock; }
    exec 6>&- 7>&-
    # A separate open file description holds the actual kernel lock, without
    # timing-dependent sleeps or a background process to clean up.
    # Allocate around descriptors used internally by the test runner's stderr
    # capture; a fixed descriptor can retain Bash's close-on-exec flag.
    exec {holder}> "$file"
    assert_ok flock -n "$holder"
    "$helper" 2> "$TEST_TMP/err"
    assert_eq "$?" 5
    assert_contains "$(cat "$TEST_TMP/err")" 'try again'
    if { : >&"$fd"; } 2>/dev/null; then _fail "$helper leaked its contended descriptor"; fi
    exec {holder}>&-
    assert_ok "$helper"
    if flock -n "$file" true; then _fail "$helper returned without holding the lock"; fi
    exec 6>&- 7>&-
    assert_ok flock -n "$file" true
  done
)
