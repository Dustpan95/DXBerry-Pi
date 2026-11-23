#!/usr/bin/env bash
# shellcheck disable=SC1091,SC2034,SC2317

session_generation_env() {
  generation_record_env
  export DXB_RUN_DIR=$TEST_TMP/run DXB_SCREENS_DIR=$TEST_TMP/run/screens DXB_APPS_STATE=$TEST_TMP/state/apps
  export DXB_SYSTEMCTL=systemctl DXB_SESSION_WAIT_S=0
  mkdir -p "$DXB_SCREENS_DIR"
  _dxb_screen_session_ok() { return 0; }
  dxb_app_is_session() { return 0; }
  dxb_radio_present() { return 0; }
  dxb_apps_wiring() { echo '{"hash":"fixture"}'; }
  dxb_apps_firstfill_done() { return 1; }
  _dxb_screen_run_settings() { : > "$TEST_TMP/settings-effect"; return 7; }
  systemctl() { printf '%s\n' "$*" >> "$TEST_TMP/service-effects"; return 0; }
}

test_session_prepare_and_start_refuse_unconfirmed_tracking() (
  local entry fault
  for entry in prepare start; do
    for fault in missing uncertain unexpected; do
      (
        session_generation_env
        local mark
        mark=$(_dxb_session_marker gamma radio1)
        mkdir -p "${mark%/*}"; : > "$mark"
        case $fault in
          missing) DXB_PROGRAM_STATE_CMD=$TEST_TMP/missing-helper ;;
          uncertain) printf '%s\n' '{"ok":true,"protocol":1}' > "$TEST_TMP/generation-output"; echo 7 > "$TEST_TMP/generation-exit" ;;
          unexpected) printf '%s\n' '{"ok":true,"protocol":1,"nonce":"private-fixture"}' > "$TEST_TMP/generation-output"; echo 0 > "$TEST_TMP/generation-exit" ;;
        esac
        if [[ $entry == prepare ]]; then dxb_screen_prepare gamma-radio1; else dxb_app_session_start gamma radio1; fi > "$TEST_TMP/out" 2> "$TEST_TMP/err"
        assert_eq "$?" 6
        [[ ! -e $TEST_TMP/settings-effect && ! -e $TEST_TMP/service-effects ]] || _fail "$entry did live work with $fault tracking"
        [[ -e $mark ]] || _fail 'tracking refusal cleared waiting marker'
        assert_not_contains "$(cat "$TEST_TMP/out" "$TEST_TMP/err")" private-fixture
      )
      rm -rf "$TEST_TMP/state" "$TEST_TMP/run" "$TEST_TMP/generation-output" "$TEST_TMP/generation-exit" "$TEST_TMP/settings-effect" "$TEST_TMP/service-effects"
    done
  done
)

test_session_attempts_track_before_later_preparation_failure() (
  session_generation_env
  dxb_screen_prepare gamma-radio1 >/dev/null 2>&1; assert_eq "$?" 7
  assert_eq "$(cat "$TEST_TMP/generation-count")" 1
  [[ -e $TEST_TMP/settings-effect ]] || _fail 'confirmed prepare did not reach settings'
  assert_ok dxb_app_session_start gamma radio1
  assert_eq "$(cat "$TEST_TMP/generation-count")" 2
)

test_session_stop_and_cleanup_ignore_unavailable_tracking() (
  session_generation_env
  DXB_PROGRAM_STATE_CMD=$TEST_TMP/missing-helper
  dxb_app_load() { return 0; }
  _dxb_screen_companions() { echo fixture-companion.service; }
  _dxb_screen_unkey() { : > "$TEST_TMP/unkey"; }
  dxb_screen_audio_stopped() { : > "$TEST_TMP/audio-cleanup"; }
  assert_ok dxb_app_session_stop gamma radio1
  assert_ok dxb_screen_stopped gamma-radio1
  assert_eq "$(cat "$TEST_TMP/generation-count")" 0
  [[ -e $TEST_TMP/unkey && -e $TEST_TMP/audio-cleanup ]] || _fail 'tracking failure blocked cleanup'
  assert_contains "$(cat "$TEST_TMP/service-effects")" 'stop fixture-companion.service'
)
