#!/usr/bin/env bash
# shellcheck disable=SC1091,SC2034,SC2317
source "$DXB_LIB/common.sh"
source "$DXB_LIB/wsjtx.sh"

native_hook_env() {
  export DXB_STATION_CMD=$TEST_TMP/native-command DXB_PROGRAM_RUN=$TEST_TMP/native-run \
    DXB_STATE_DIR=$TEST_TMP/state INVOCATION_ID=123456789abcdef0123456789abcdef0 DXB_LOG_FILE=/dev/null
  cat > "$DXB_STATION_CMD" <<'SH'
#!/bin/bash
printf '%s\n' "$@" >> "$TEST_TMP/native-args"
[[ ! -f $TEST_TMP/appcalls ]] || cp "$TEST_TMP/appcalls" "$TEST_TMP/before-native"
[[ -z ${DXB_SESSIONS_DIR:-} || ! -d $DXB_SESSIONS_DIR/wsjtx-radio2.d ]] && touch "$TEST_TMP/session-dir-absent"
[[ ! -f $TEST_TMP/native-fails ]] || exit 8
SH
  chmod 755 "$DXB_STATION_CMD"
}

test_wsjtx_hook_forwards_exact_invocation_and_configuration() (
  native_hook_env
  assert_ok dxb_wsjtx_hook start wsjtx-radio2
  assert_eq "$(cat "$TEST_TMP/native-args")" "$(printf '%s\n' -state "$DXB_STATE_DIR/station" -program-run "$DXB_PROGRAM_RUN" -wsjtx-hook start -wsjtx-radio radio2 -wsjtx-invocation "$INVOCATION_ID")"
)

test_wsjtx_hook_skips_other_programs_and_refuses_invalid_names() (
  native_hook_env
  assert_ok dxb_wsjtx_hook start gamma-radio2
  assert_ok test ! -e "$TEST_TMP/native-args"
  dxb_wsjtx_hook start wsjtx-../radio2; assert_eq "$?" 2
  dxb_wsjtx_hook start wsjtx-radio-2; assert_eq "$?" 2
  assert_ok test ! -e "$TEST_TMP/native-args"
)

test_wsjtx_hook_failure_blocks_start_without_editing_settings() (
  native_hook_env
  touch "$TEST_TMP/native-fails"
  dxb_wsjtx_hook start wsjtx-radio2; assert_eq "$?" 7
  assert_file_contains "$TEST_TMP/native-args" start
  assert_ok test ! -e "$DXB_STATE_DIR/station/radios/radio2.json"
)

# Reuse the full program-session fixture with an inert WSJT-X-shaped module.
# No real program, sound device or transmitter is started.
native_session_env() {
  sc_env
  native_hook_env
  sed 's/app_gamma_/app_wsjtx_/g' "$DXB_APPS_DIR/gamma.sh" > "$DXB_APPS_DIR/wsjtx.sh"
  cat > "$TEST_TMP/bin/dpkg-query" <<'SH'
#!/bin/bash
case ${*: -1} in gamma|wsjtx) echo 'installed 1.0'; exit 0 ;; *) exit 1 ;; esac
SH
  chmod 755 "$TEST_TMP/bin/dpkg-query"
  sc_owned radio2 wsjtx
}

test_wsjtx_prepare_runs_native_hook_after_firstfill_and_wiring() (
  native_session_env
  assert_ok sc_app prepare wsjtx-radio2
  assert_file_contains "$TEST_TMP/before-native" 'gamma firstfill radio2'
  assert_file_contains "$TEST_TMP/before-native" 'gamma wire radio2'
  assert_file_contains "$TEST_TMP/native-args" start
  assert_ok test -f "$DXB_SESSIONS_DIR/wsjtx-radio2.env"
)

test_wsjtx_prepare_failure_never_calls_native_hook() (
  native_session_env
  export GAMMA_FAIL=wire
  sc_app prepare wsjtx-radio2; assert_eq "$?" 7
  assert_ok test ! -e "$TEST_TMP/native-args"
)

test_wsjtx_stopped_cleans_session_even_when_native_recovery_fails() (
  native_session_env
  assert_ok sc_app prepare wsjtx-radio2
  rm -f "$TEST_TMP/native-args" "$TEST_TMP/session-dir-absent"
  touch "$TEST_TMP/native-fails"
  sc_app stopped wsjtx-radio2; assert_eq "$?" 7
  assert_file_contains "$TEST_TMP/native-args" stopped
  assert_ok test -f "$TEST_TMP/session-dir-absent"
  assert_ok test ! -e "$DXB_SESSIONS_DIR/wsjtx-radio2.d"
)

test_wsjtx_stopped_still_requests_recovery_after_record_load_failure() (
  native_session_env
  printf '{broken\n' > "$DXB_RADIOS_FILE"
  assert_ok sc_app stopped wsjtx-radio2
  assert_file_contains "$TEST_TMP/native-args" stopped
)

test_wsjtx_presence_preview_never_queues_an_unplugged_program() (
  native_session_env
  sc_unplug radio2
  sc_app verify-present wsjtx-radio2 --json > "$TEST_TMP/presence"; assert_eq "$?" 1
  assert_file_contains "$TEST_TMP/presence" '"present":false'
  assert_ok test ! -e "$DXB_SESSIONS_DIR/wsjtx-radio2.waiting"
  assert_ok test ! -e "$TEST_TMP/native-args"
  # The actual systemd condition keeps its existing hotplug retry behavior.
  sc_app present wsjtx-radio2 --json > /dev/null; assert_eq "$?" 1
  assert_ok test -e "$DXB_SESSIONS_DIR/wsjtx-radio2.waiting"
)

test_build_requires_native_program_hook_library() (
  local required
  required=$(sed -n '/^REQUIRED_FILES=(/,/^)/p' "$DXB_ROOT/build/build-image.sh")
  assert_contains "$required" provision/lib/wsjtx.sh
)
