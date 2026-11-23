#!/usr/bin/env bash
# Synthetic empty registration only; no device or service work runs.
# shellcheck disable=SC1090,SC1091,SC2030,SC2031,SC2034,SC2317

radio_publication_env() {
  export DXB_STATE_DIR=$TEST_TMP/state DXB_RUN_DIR=$TEST_TMP/run DXB_LOG_FILE=/dev/null
  export DXB_RADIOS_FILE=$TEST_TMP/state/radios.json DXB_FLOCK=flock DXB_RADIO_APPLY_LOCK_WAIT=0
  export DXB_PROGRAM_STATE_CMD=$DXB_ROOT/tests/fixtures/program-state.sh
  mkdir -p "$DXB_STATE_DIR" "$DXB_RUN_DIR"
  source "$DXB_LIB/common.sh"
  source "$DXB_LIB/radio.sh"
  dxb_radio_empty_record | jq -S . > "$DXB_RADIOS_FILE"
  dxb_radio_load
}

test_radio_publication_lock_refuses_contended_save_without_changing_record_or_cache() (
  radio_publication_env
  local before=$DXB_RADIOS candidate holder
  candidate=$(jq -c '.gps.baud=4800' <<< "$before")
  cp "$DXB_RADIOS_FILE" "$TEST_TMP/before"
  exec {holder}> "$DXB_RUN_DIR/apply.lock"
  command flock -x "$holder"
  dxb_radio_save "$candidate" 2> "$TEST_TMP/err"
  assert_eq "$?" 6
  assert_eq "$DXB_RADIOS" "$before"
  assert_ok cmp "$DXB_RADIOS_FILE" "$TEST_TMP/before"
  exec {holder}>&-
  assert_ok dxb_radio_save "$candidate"
  assert_eq "$DXB_RADIOS" "$(jq -c . "$DXB_RADIOS_FILE")"
  assert_eq "$(jq -r .gps.baud "$DXB_RADIOS_FILE")" 4800
  assert_ok command flock -n "$DXB_RUN_DIR/apply.lock" true
)

test_radio_publication_lock_covers_replace_readback_and_cache_update() (
  radio_publication_env
  publication_probe() {
    echo "$1" >> "$TEST_TMP/phases"
    if command flock -n "$DXB_RUN_DIR/apply.lock" true; then _fail "$1 escaped apply exclusion"; fi
  }
  mv() {
    if [[ ${*: -1} == "$DXB_RADIOS_FILE" ]]; then publication_probe replace; fi
    command mv "$@"
  }
  jq() {
    if [[ $* == '-c .' ]]; then publication_probe cache; fi
    command jq "$@"
  }
  local candidate
  candidate=$(command jq -c '.gps.baud=4800' <<< "$DXB_RADIOS")
  assert_ok dxb_radio_save "$candidate"
  assert_contains "$(cat "$TEST_TMP/phases")" replace
  assert_contains "$(cat "$TEST_TMP/phases")" cache
  assert_eq "$DXB_RADIOS" "$(command jq -c . "$DXB_RADIOS_FILE")"
  if { : >&9; } 2>/dev/null; then _fail 'save retained its apply descriptor'; fi
  assert_ok command flock -n "$DXB_RUN_DIR/apply.lock" true
)

test_radio_publication_lock_failed_replace_closes_gate_and_preserves_old_state() (
  radio_publication_env
  local before=$DXB_RADIOS candidate
  candidate=$(jq -c '.gps.baud=4800' <<< "$before")
  cp "$DXB_RADIOS_FILE" "$TEST_TMP/before"
  mv() {
    if [[ ${*: -1} == "$DXB_RADIOS_FILE" ]]; then
      if command flock -n "$DXB_RUN_DIR/apply.lock" true; then _fail 'failed replace escaped apply exclusion'; fi
      return 1
    fi
    command mv "$@"
  }
  dxb_radio_save "$candidate" 2> "$TEST_TMP/err"
  assert_eq "$?" 6
  assert_eq "$DXB_RADIOS" "$before"
  assert_ok cmp "$DXB_RADIOS_FILE" "$TEST_TMP/before"
  assert_ok command flock -n "$DXB_RUN_DIR/apply.lock" true
)

test_radio_publication_lock_unavailable_gate_refuses_but_invalid_input_stays_usage_error() (
  local base=$TEST_TMP fault before candidate
  for fault in missing failed open directory; do
    TEST_TMP=$base/$fault; export TEST_TMP
    radio_publication_env
    before=$DXB_RADIOS
    candidate=$(jq -c '.gps.baud=4800' <<< "$before")
    cp "$DXB_RADIOS_FILE" "$TEST_TMP/before"
    case $fault in
      missing) DXB_FLOCK=$TEST_TMP/missing-flock ;;
      failed) flock() { return 1; } ;;
      open) mkdir "$DXB_RUN_DIR/apply.lock" ;;
      directory) rmdir "$DXB_RUN_DIR"; : > "$DXB_RUN_DIR" ;;
    esac
    dxb_radio_save "$candidate" 2> "$TEST_TMP/err"
    assert_eq "$?" 6
    assert_eq "$DXB_RADIOS" "$before"
    assert_ok cmp "$DXB_RADIOS_FILE" "$TEST_TMP/before"
    dxb_radio_save not-json 2> /dev/null; assert_eq "$?" 2
    if { : >&9; } 2>/dev/null; then _fail "$fault retained its apply descriptor"; fi
    unset -f flock
  done
)
