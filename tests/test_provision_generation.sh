#!/usr/bin/env bash
# shellcheck disable=SC1090,SC1091,SC2034,SC2317

provision_tracking_fixture() {
  full_env
  printf 'PASSWORD=examplepass\nGPS_DEVICE=none\n' > "$DXB_BOOT_DIR/dxberry.txt"
  export DXB_PROGRAM_STATE_CMD=$TEST_TMP/tracker
  cat > "$DXB_PROGRAM_STATE_CMD" <<'HELPER'
#!/bin/bash
[[ $# == 3 && $1 == --state && $2 == "$DXB_STATE_DIR/station" && $3 == prepare-advance ]] || exit 2
printf '%s\n' "$3" >> "$TEST_TMP/tracking-calls"
if [[ -e $TEST_TMP/tracking-output ]]; then cat "$TEST_TMP/tracking-output"; exit "$(cat "$TEST_TMP/tracking-exit")"; fi
printf '%s\n' '{"ok":true,"protocol":1}'
HELPER
  chmod 755 "$DXB_PROGRAM_STATE_CMD"
}

provision_tracking_refusal() (
  local mode=$1 fault=$2
  provision_tracking_fixture
  case $fault in
    missing) DXB_PROGRAM_STATE_CMD=$TEST_TMP/missing-helper ;;
    uncertain) printf '%s\n' '{"ok":true,"protocol":1}' > "$TEST_TMP/tracking-output"; echo 7 > "$TEST_TMP/tracking-exit" ;;
    unexpected) printf '%s\n' '{"ok":true,"protocol":1,"nonce":"private-fixture"}' > "$TEST_TMP/tracking-output"; echo 0 > "$TEST_TMP/tracking-exit" ;;
  esac
  local -a args=()
  [[ $mode == run ]] || args+=("--$mode")
  (
    source "$DXB_ROOT/provision/bin/dxberry-provision"
    dxb_require_root() { :; }
    provision_system() { : > "$TEST_TMP/live-effect"; exit 73; }
    main "${args[@]}"
  ) > "$TEST_TMP/out" 2> "$TEST_TMP/err"
  assert_eq "$?" 6
  [[ ! -e $TEST_TMP/live-effect ]] || _fail "$mode reached system work after $fault tracking"
  [[ ! -e $DXB_STATE_DIR/provisioned ]] || _fail 'tracking refusal marked provisioning complete'
  [[ ! -e $DXB_STATE_DIR/status.txt ]] || _fail 'tracking refusal published a completed status'
  assert_not_contains "$(cat "$TEST_TMP/calls" 2>/dev/null)" reboot
  assert_not_contains "$(cat "$TEST_TMP/out" "$TEST_TMP/err")" private-fixture
)

test_provision_requires_tracking_before_live_steps() (
  local mode fault
  for mode in run reseed first-boot; do
    for fault in missing uncertain unexpected; do
      TEST_TMP="$TEST_TMP/$mode-$fault" provision_tracking_refusal "$mode" "$fault"
    done
  done
)

test_provision_readonly_and_completed_first_boot_do_not_track() (
  provision_tracking_fixture
  (
    source "$DXB_ROOT/provision/bin/dxberry-provision"
    dxb_require_root() { :; }
    main --check >/dev/null
    assert_eq "$?" 0
    main --help >/dev/null
    assert_eq "$?" 0
    mkdir -p "$DXB_STATE_DIR"
    : > "$DXB_STATE_DIR/provisioned"
    main --first-boot >/dev/null
    assert_eq "$?" 0
  )
  [[ ! -e $TEST_TMP/tracking-calls ]] || _fail 'read-only/no-op request advanced tracking'
)

test_provision_tracks_before_failed_first_step_and_retries() (
  provision_tracking_fixture
  local attempt
  for attempt in 1 2; do
    (
      source "$DXB_ROOT/provision/bin/dxberry-provision"
      dxb_require_root() { :; }
      provision_system() {
        assert_eq "$(wc -l < "$TEST_TMP/tracking-calls")" "$attempt"
        : > "$TEST_TMP/live-effect"
        exit 73
      }
      main
    ) >/dev/null 2> "$TEST_TMP/err"
    assert_eq "$?" 73
    assert_eq "$(wc -l < "$TEST_TMP/tracking-calls" 2>/dev/null || echo 0)" "$attempt"
  done
  [[ -e $TEST_TMP/live-effect ]] || _fail 'confirmed tracking did not reach the first step'
)
