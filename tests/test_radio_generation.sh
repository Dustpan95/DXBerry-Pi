#!/usr/bin/env bash
# shellcheck disable=SC1091,SC2034,SC2317
source "$DXB_LIB/common.sh"
source "$DXB_LIB/radio.sh"

generation_record_env() {
  export DXB_STATE_DIR=$TEST_TMP/state DXB_RADIOS_FILE=$TEST_TMP/state/radios.json DXB_LOG_FILE=/dev/null
  export DXB_RUN_DIR=$TEST_TMP/run DXB_FLOCK=flock DXB_RADIO_APPLY_LOCK_WAIT=0
  export DXB_PROGRAM_STATE_CMD=$TEST_TMP/generation-helper
  mkdir -p "$DXB_STATE_DIR/station"
  cat > "$DXB_PROGRAM_STATE_CMD" <<'HELPER'
#!/bin/bash
[[ $# == 3 && $1 == --state && $2 == "$TEST_TMP/state/station" && $3 == advance ]] || exit 2
count=$(cat "$TEST_TMP/generation-count" 2>/dev/null || echo 0)
count=$((count+1))
printf '%s\n' "$count" > "$TEST_TMP/generation-count"
if [[ -f $TEST_TMP/generation-fail-at && $count == "$(cat "$TEST_TMP/generation-fail-at")" ]]; then
  printf '%s\n' '{"ok":true,"protocol":1}'
  exit 7
fi
if [[ -f $TEST_TMP/generation-output ]]; then cat "$TEST_TMP/generation-output"; exit "$(cat "$TEST_TMP/generation-exit")"; fi
printf '%s\n' '{"ok":true,"protocol":1}'
HELPER
  chmod 755 "$DXB_PROGRAM_STATE_CMD"
  dxb_radio_load
  local record='{"version":1,"radios":{"radio1":{"label":"fixture","audio":null,"cat":{"path":"usb-0:1.1:1.0","vidpid":"1234:abcd","serial":"fixture"},"hid":null,"ptt_serial":null,"ptt":{"method":"none","gpio_line":null},"rig":{"model":1,"baud":0,"ptt_type":"NONE"},"rigctld_port":4532,"wiring":"full","owner":""}}}'
  dxb_radio_save "$record" || _fail "fixture record initialization failed"
  printf '0\n' > "$TEST_TMP/generation-count"
}

test_radio_save_refuses_failed_or_unexpected_generation_responses() (
  generation_record_env
  local before=$DXB_RADIOS row body code
  row=$(jq -c '.radios.radio1.label="changed"' <<< "$DXB_RADIOS")
  for body in '{"ok":true,"protocol":1}' '{"ok":false,"protocol":1}' 'not-json' '{"ok":true,"protocol":1,"nonce":"private-fixture"}' ''; do
    printf '%s\n' "$body" > "$TEST_TMP/generation-output"
    code=0; [[ $body != '{"ok":true,"protocol":1}' ]] || code=7
    printf '%s\n' "$code" > "$TEST_TMP/generation-exit"
    dxb_radio_save "$row" > "$TEST_TMP/out" 2> "$TEST_TMP/err"; assert_eq "$?" 6
    assert_eq "$DXB_RADIOS" "$before"
    assert_eq "$(jq -c . "$DXB_RADIOS_FILE")" "$before"
    assert_not_contains "$(cat "$TEST_TMP/err")" private-fixture
    assert_eq "$(cat "$TEST_TMP/out")" ''
  done
  rm "$TEST_TMP/generation-output" "$TEST_TMP/generation-exit"
  DXB_PROGRAM_STATE_CMD=$TEST_TMP/missing-helper dxb_radio_save "$row" 2> /dev/null; assert_eq "$?" 6
  assert_eq "$(jq -c . "$DXB_RADIOS_FILE")" "$before"
)

test_radio_save_validates_before_advancing_generation() (
  generation_record_env
  dxb_radio_save not-json 2> /dev/null; assert_eq "$?" 2
  dxb_radio_save '{"version":2,"radios":{}}' 2> /dev/null; assert_eq "$?" 2
  assert_eq "$(cat "$TEST_TMP/generation-count")" 0
)

test_radio_owner_changes_and_returns_advance_generation() (
  generation_record_env
  local before=$DXB_RADIOS
  assert_ok _dxb_radio_set_owner radio1 wsjtx
  assert_ok _dxb_radio_set_owner radio1 ''
  assert_eq "$DXB_RADIOS" "$before"
  assert_eq "$(cat "$TEST_TMP/generation-count")" 2
)

test_radio_handover_refuses_tracking_failure_before_control_work() (
  generation_record_env
  assert_ok _dxb_radio_set_owner radio1 alpha
  printf '%s\n' '{"ok":false,"protocol":1}' > "$TEST_TMP/generation-output"
  printf '6\n' > "$TEST_TMP/generation-exit"
  dxb_app_load_owner() { : > "$TEST_TMP/control-work"; return 0; }
  dxb_app_is_session() { return 1; }
  dxb_radio_present() { return 0; }
  dxb_radio_owner_kind() { echo graywolf; }
  _dxb_radio_let_go() { : > "$TEST_TMP/control-work"; return 0; }
  _dxb_radio_settle() { : > "$TEST_TMP/control-work"; return 0; }
  dxb_audio_sync() { : > "$TEST_TMP/control-work"; return 0; }
  dxb_app_start() { : > "$TEST_TMP/control-work"; return 0; }
  dxb_app_wire() { : > "$TEST_TMP/control-work"; return 0; }
  _dxb_radio_mark_wired() { return 0; }
  local before=$DXB_RADIOS
  dxb_radio_claim radio1 beta > /dev/null 2>&1; assert_eq "$?" 6
  [[ ! -e $TEST_TMP/control-work ]] || _fail "claim changed controls before generation refusal"
  dxb_radio_release radio1 > /dev/null 2>&1; assert_eq "$?" 6
  [[ ! -e $TEST_TMP/control-work ]] || _fail "release changed controls before generation refusal"
  assert_eq "$DXB_RADIOS" "$before"
)

generation_failed_claim() (
  local kind=$1
  generation_record_env
  printf '3\n' > "$TEST_TMP/generation-fail-at"
  dxb_app_load_owner() { return 0; }
  dxb_radio_present() { return 0; }
  dxb_app_is_session() { [[ $kind == session ]]; }
  dxb_app_is_multi() { return 0; }
  dxb_radio_alsa_levels() { :; }
  _dxb_radio_rigctld_for() { printf '%s\n' "$2" >> "$TEST_TMP/rig-kinds"; }
  dxb_app_session_start() { return 1; }
  dxb_app_session_stop() { : > "$TEST_TMP/cleanup"; }
  dxb_audio_sync() { :; }
  dxb_app_start() { return 1; }
  dxb_app_wire() { return 1; }
  dxb_app_unwire() { : > "$TEST_TMP/cleanup"; }
  dxb_app_stop_if_idle() { : > "$TEST_TMP/cleanup"; }
  dxb_radio_claim radio1 gamma > "$TEST_TMP/out" 2> "$TEST_TMP/err"; assert_eq "$?" 8
  assert_eq "$(jq -r '.radios.radio1.owner' "$DXB_RADIOS_FILE")" gamma
  assert_eq "$(jq -r '.radios.radio1.owner' <<< "$DXB_RADIOS")" gamma
  [[ -e $TEST_TMP/cleanup ]] || _fail "tracking failure blocked owed cleanup"
  assert_not_contains "$(cat "$TEST_TMP/err")" 'left released'
  assert_contains "$(cat "$TEST_TMP/err")" 'release could not be confirmed'
  if [[ $kind == session ]]; then assert_eq "$(cat "$TEST_TMP/rig-kinds")" session; fi
)

test_radio_failed_session_claim_reports_unconfirmed_release() { generation_failed_claim session; }
test_radio_failed_shared_claim_reports_unconfirmed_release() { generation_failed_claim shared; }

generation_apply_env() {
  generation_record_env
  export DXB_RUN_DIR=$TEST_TMP/run DXB_RADIO_APPLY_LOCK_WAIT=0 DXB_FLOCK=flock
  mkdir -p "$DXB_RUN_DIR"
  cat > "$TEST_TMP/locked-helper" <<'HELPER'
#!/bin/bash
if flock -n "$DXB_RUN_DIR/apply.lock" true; then
  : > "$TEST_TMP/unlocked-generation"
  exit 6
fi
exec "$TEST_TMP/generation-helper" "$@"
HELPER
  chmod 755 "$TEST_TMP/locked-helper"
  DXB_PROGRAM_STATE_CMD=$TEST_TMP/locked-helper
  # Only the record reader, apply coordinator, real kernel lock and metadata
  # helper are exercised. Every scanner/device/service action is a marker.
  dxb_radio_scan_cache() { generation_apply_effect scan; DXB_RADIO_SCAN='[]'; }
  dxb_radio_udev_write() { generation_apply_effect udev; }
  dxb_radio_modprobe_install() { generation_apply_effect modprobe; }
  dxb_radio_present() { return 1; }
  _dxb_radio_apply_flrig() { generation_apply_effect flrig; }
  dxb_rigctld_sync() { generation_apply_effect rigctld; }
  dxb_audio_sync() { generation_apply_effect audio; }
  dxb_rigctld_stop_all_except() { generation_apply_effect orphan-rigctld; }
  dxb_audio_stop_all_except() { generation_apply_effect orphan-audio; }
  dxb_radio_write_state() {
    generation_apply_effect state
    [[ ! -e $TEST_TMP/fail-state ]]
  }
}

generation_apply_effect() {
  printf '%s\n' "$1" >> "$TEST_TMP/apply-effects"
  if [[ $(cat "$TEST_TMP/generation-count") == 0 ]]; then
    : > "$TEST_TMP/untracked-effect"
  fi
  if flock -n "$DXB_RUN_DIR/apply.lock" true; then
    : > "$TEST_TMP/unlocked-effect"
  fi
}

test_radio_apply_refuses_tracking_failure_before_derived_work() (
  generation_apply_env
  local before=$DXB_RADIOS mode body code
  for mode in full hotplug; do
    for body in '{"ok":true,"protocol":1}' '{"ok":false,"protocol":1}' 'not-json' '{"ok":true,"protocol":1,"nonce":"private-fixture"}' ''; do
      printf '%s\n' "$body" > "$TEST_TMP/generation-output"
      code=0; [[ $body != '{"ok":true,"protocol":1}' ]] || code=7
      printf '%s\n' "$code" > "$TEST_TMP/generation-exit"
      dxb_radio_apply "$mode" > "$TEST_TMP/out" 2> "$TEST_TMP/err"; assert_eq "$?" 6
      [[ ! -e $TEST_TMP/apply-effects ]] || _fail "$mode applied without confirmed generation"
      assert_eq "$DXB_RADIOS" "$before"
      assert_eq "$(jq -c . "$DXB_RADIOS_FILE")" "$before"
      assert_eq "$(cat "$TEST_TMP/out")" ''
      assert_not_contains "$(cat "$TEST_TMP/err")" private-fixture
      assert_ok flock -n "$DXB_RUN_DIR/apply.lock" true
    done
    DXB_PROGRAM_STATE_CMD=$TEST_TMP/missing-helper dxb_radio_apply "$mode" 2>/dev/null; assert_eq "$?" 6
    [[ ! -e $TEST_TMP/apply-effects ]] || _fail "$mode applied with a missing helper"
  done
  [[ ! -e $TEST_TMP/unlocked-generation ]] || _fail 'helper ran outside apply lock'
)

test_radio_apply_rejects_invalid_json_before_generation() (
  generation_apply_env
  printf 'not-json\n' > "$DXB_RADIOS_FILE"
  dxb_radio_apply hotplug 2>/dev/null; assert_eq "$?" 6
  assert_eq "$(cat "$TEST_TMP/generation-count")" 0
  [[ ! -e $TEST_TMP/apply-effects ]] || _fail 'invalid record reached derived work'
  assert_ok flock -n "$DXB_RUN_DIR/apply.lock" true
)

test_radio_apply_advances_without_record_changes_and_keeps_failed_attempts() (
  generation_apply_env
  local before=$DXB_RADIOS
  assert_ok dxb_radio_apply
  assert_eq "$(cat "$TEST_TMP/generation-count")" 1
  assert_contains "$(cat "$TEST_TMP/apply-effects")" udev
  rm "$TEST_TMP/apply-effects"
  assert_ok dxb_radio_apply hotplug
  assert_eq "$(cat "$TEST_TMP/generation-count")" 2
  assert_not_contains "$(cat "$TEST_TMP/apply-effects")" udev
  : > "$TEST_TMP/fail-state"
  dxb_radio_apply hotplug 2>/dev/null; assert_eq "$?" 6
  assert_eq "$(cat "$TEST_TMP/generation-count")" 3
  rm "$TEST_TMP/fail-state"
  assert_ok dxb_radio_apply hotplug
  assert_eq "$(cat "$TEST_TMP/generation-count")" 4
  assert_eq "$DXB_RADIOS" "$before"
  assert_eq "$(jq -c . "$DXB_RADIOS_FILE")" "$before"
  [[ ! -e $TEST_TMP/untracked-effect ]] || _fail 'derived work ran before advancing'
  [[ ! -e $TEST_TMP/unlocked-effect ]] || _fail 'derived work ran outside apply lock'
  [[ ! -e $TEST_TMP/unlocked-generation ]] || _fail 'helper ran outside apply lock'
)

test_radio_apply_lock_failure_never_advances_generation() (
  generation_apply_env
  local holder
  exec {holder}> "$DXB_RUN_DIR/apply.lock"
  assert_ok flock -n "$holder"
  dxb_radio_apply hotplug 2>/dev/null; assert_eq "$?" 6
  assert_eq "$(cat "$TEST_TMP/generation-count")" 0
  [[ ! -e $TEST_TMP/apply-effects ]] || _fail 'contended apply did derived work'
  exec {holder}>&-
  assert_ok dxb_radio_apply hotplug
  assert_eq "$(cat "$TEST_TMP/generation-count")" 1
)
