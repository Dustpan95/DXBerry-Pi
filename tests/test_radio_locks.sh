#!/usr/bin/env bash
# shellcheck disable=SC1090,SC1091,SC2034,SC2317

# Exercise the actual operation boundary. The downstream record/application/apply
# implementation is a marker: it must never run without the required lock.
radio_lock_refusal_fixture() (
  local entry=$1 fault=$2 expected=5 fd=8
  export DXB_STATE_DIR=$TEST_TMP/state DXB_RUN_DIR=$TEST_TMP/run
  export DXB_LOG_FILE=$TEST_TMP/log DXB_SYSTEMCTL=systemctl
  mkdir -p "$DXB_STATE_DIR" "$DXB_RUN_DIR"
  exec 8>&- 9>&-
  case $entry in
    radio)
      source "$DXB_ROOT/provision/bin/dxberry-radio"
      expected=6
      dxb_require_root() { :; }
      load_config_if_any() { :; }
      dxb_radio_load() { : > "$TEST_TMP/unguarded"; return 6; }
      ;;
    session|stop)
      source "$DXB_ROOT/provision/bin/dxberry-app"
      dxb_radio_load() { : > "$TEST_TMP/unguarded"; return 6; }
      ;;
    remove|apply)
      source "$DXB_LIB/common.sh"
      source "$DXB_LIB/radio.sh"
      source "$DXB_LIB/apps.sh"
      _dxb_apps_remove() { : > "$TEST_TMP/unguarded"; }
      _dxb_radio_apply() { : > "$TEST_TMP/unguarded"; }
      [[ $entry == apply ]] && { expected=6; fd=9; }
      ;;
  esac
  local lock=$DXB_RUN_DIR/record.lock
  [[ $entry == apply ]] && lock=$DXB_RUN_DIR/apply.lock
  DXB_FLOCK=flock
  case $fault in
    missing)
      # Hide only the command-availability probe; all other tools stay real.
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
    radio) main set radio1 --label changed ;;
    session) cmd_session_ctl start radio1 ;;
    stop) cmd_session_ctl stop radio1 ;;
    remove) dxb_apps_remove gamma ;;
    apply) dxb_radio_apply hotplug ;;
  esac > "$TEST_TMP/out" 2> "$TEST_TMP/err"
  assert_eq "$?" "$expected"
  [[ ! -e $TEST_TMP/unguarded ]] || _fail "$entry ran without its $fault lock"
  if [[ $fault == busy ]]; then
    assert_contains "$(cat "$TEST_TMP/err")" "try again"
  else
    assert_contains "$(cat "$TEST_TMP/err")" lock
  fi
  if { : >&"$fd"; } 2>/dev/null; then _fail "$entry left descriptor $fd open after $fault failure"; fi
)

test_radio_record_changes_require_an_available_lock() (
  local fault
  for fault in missing open directory busy; do
    radio_lock_refusal_fixture radio "$fault"
    rm -rf "$TEST_TMP/run" "$TEST_TMP/unguarded"
  done
)

test_program_start_requires_an_available_record_lock() (
  local fault
  for fault in missing open directory busy; do
    radio_lock_refusal_fixture session "$fault"
    rm -rf "$TEST_TMP/run" "$TEST_TMP/unguarded"
  done
)

test_program_removal_requires_an_available_record_lock() (
  local fault
  for fault in missing open directory busy; do
    radio_lock_refusal_fixture remove "$fault"
    rm -rf "$TEST_TMP/run" "$TEST_TMP/unguarded"
  done
)

test_program_stop_requires_an_available_record_lock() (
  local fault
  for fault in missing open directory busy; do
    radio_lock_refusal_fixture stop "$fault"
    rm -rf "$TEST_TMP/run" "$TEST_TMP/unguarded"
  done
)

test_radio_apply_requires_an_available_lock() (
  local fault
  for fault in missing open directory busy; do
    radio_lock_refusal_fixture apply "$fault"
    rm -rf "$TEST_TMP/run" "$TEST_TMP/unguarded"
  done
)
