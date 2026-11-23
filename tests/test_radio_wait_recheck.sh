#!/usr/bin/env bash
# Synthetic sysfs changes at explicit service-wait boundaries; no radio is used.
# shellcheck disable=SC2030,SC2031,SC2034,SC2317

radio_recheck_fault() {
  case ${RECHECK_FAULT:-unplug} in
    unplug) fx_unplug "$DXB_SYSFS_ROOT" r1-cat ;;
    replacement) printf 'replacement\n' > "$DXB_SYSFS_ROOT/$FX_USB_BASE/1-1.4/serial" ;;
    renumber)
      fx_unplug "$DXB_SYSFS_ROOT" r1-cat
      fx_usb_function "$DXB_SYSFS_ROOT" 1-1.4 0 serial ttyUSB9 ;;
  esac
}

radio_recheck_env() {
  radio_env; fake_apps; two_radios
  export RECHECK_PHASE=ready RECHECK_FAULT=unplug RECHECK_START_RC=0
  cat >> "$DXB_APPS_DIR/alpha.sh" <<'MODULE'
app_alpha_wait_ready() {
  [[ $RECHECK_PHASE != ready ]] || radio_recheck_fault
  return "$RECHECK_START_RC"
}
app_alpha_wire() {
  echo "alpha wire $1" >> "$TEST_TMP/appcalls"
  [[ $RECHECK_PHASE != wire ]] || radio_recheck_fault
  return 0
}
MODULE
}

test_radio_wait_recheck_new_shared_claim_releases_after_ready_or_wire_changes() (
  local base=$TEST_TMP phase fault
  for phase in ready wire; do
    for fault in unplug replacement; do
      TEST_TMP=$base/$phase-$fault; export TEST_TMP
      radio_recheck_env; RECHECK_PHASE=$phase; RECHECK_FAULT=$fault
      local rc
      dxb_radio_claim r1 alpha > "$TEST_TMP/out" 2> "$TEST_TMP/err"; rc=$?
      assert_eq "$rc" 5
      assert_eq "$(jq -r .radios.r1.owner "$DXB_RADIOS_FILE")" ''
      assert_contains "$(cat "$TEST_TMP/appcalls")" 'alpha unwire r1'
      [[ $phase != ready ]] || assert_not_contains "$(cat "$TEST_TMP/appcalls")" 'alpha wire r1'
      assert_not_contains "$(cat "$TEST_TMP/out")" 'now owned'
      assert_eq "$(_dxb_radio_wired_get r1)" ''
    done
  done
)

test_radio_wait_recheck_same_owner_keeps_intent_without_claiming_fresh_success() (
  local base=$TEST_TMP phase
  for phase in ready wire; do
    TEST_TMP=$base/$phase; export TEST_TMP
    radio_recheck_env; RECHECK_PHASE=$phase
    _dxb_radio_set_owner r1 alpha
    dxb_radio_claim r1 alpha > "$TEST_TMP/out" 2> "$TEST_TMP/err"; assert_eq "$?" 4
    assert_eq "$(jq -r .radios.r1.owner "$DXB_RADIOS_FILE")" alpha
    [[ $phase != ready ]] || assert_not_contains "$(cat "$TEST_TMP/appcalls")" 'alpha wire r1'
    assert_eq "$(_dxb_radio_wired_get r1)" ''
  done
)

test_radio_wait_recheck_session_activation_checks_new_and_existing_owners() (
  local base=$TEST_TMP same
  for same in no yes; do
    TEST_TMP=$base/$same; export TEST_TMP
    radio_recheck_env
    [[ $same != yes ]] || _dxb_radio_set_owner r1 gamma
    _dxb_radio_rigctld_for() { echo "$2" >> "$TEST_TMP/rig-kinds"; }
    dxb_app_session_start() { radio_recheck_fault; return 0; }
    local rc
    dxb_radio_claim r1 gamma > "$TEST_TMP/out" 2> "$TEST_TMP/err"; rc=$?
    if [[ $same == no ]]; then
      assert_eq "$rc" 5
      assert_eq "$(jq -r .radios.r1.owner "$DXB_RADIOS_FILE")" ''
      assert_contains "$(cat "$TEST_TMP/calls")" 'disable --now dxberry-screen@gamma-r1.service'
    else
      assert_eq "$rc" 4
      assert_eq "$(jq -r .radios.r1.owner "$DXB_RADIOS_FILE")" gamma
    fi
    assert_not_contains "$(cat "$TEST_TMP/out")" 'now owned'
  done
)

test_radio_wait_recheck_old_owner_wait_cannot_publish_a_new_target_after_unplug() (
  local base=$TEST_TMP target
  for target in beta gamma; do
    TEST_TMP=$base/$target; export TEST_TMP
    radio_recheck_env
    _dxb_radio_set_owner r1 alpha
    if [[ $target == gamma ]]; then
      _dxb_radio_set_owner r2 alpha
      echo alpha.service > "$TEST_TMP/active"
    fi
    systemctl() {
      if [[ $target == beta && $1 == stop && ${*: -1} == alpha.service ]]; then radio_recheck_fault; fi
      fx_systemctl "$@"
    }
    _dxb_radio_rigctld_for() { echo "$2" >> "$TEST_TMP/rig-kinds"; }
    dxb_app_session_start() { : > "$TEST_TMP/session-start"; }
    dxb_radio_claim r1 "$target" > "$TEST_TMP/out" 2> "$TEST_TMP/err"; assert_eq "$?" 4
    assert_eq "$(jq -r .radios.r1.owner "$DXB_RADIOS_FILE")" ''
    assert_not_contains "$(cat "$TEST_TMP/appcalls")" 'beta wire'
    [[ ! -e $TEST_TMP/session-start ]] || _fail 'started a session after old-owner wait lost the device'
    [[ ! -e $TEST_TMP/rig-kinds ]] || _fail 'routed a new session after old-owner wait lost the device'
  done
)

test_radio_wait_recheck_failed_start_does_not_restart_audio_for_a_lost_device() (
  local base=$TEST_TMP app
  for app in alpha gamma; do
    TEST_TMP=$base/$app; export TEST_TMP
    radio_recheck_env; RECHECK_START_RC=1
    dxb_audio_sync() { printf '%s:%s\n' "$1" "$2" >> "$TEST_TMP/audio"; }
    _dxb_radio_rigctld_for() {
      local present=0; dxb_radio_present "$1" && present=1
      printf '%s:%s:%s\n' "$1" "$2" "$present" >> "$TEST_TMP/rig-kinds"
    }
    dxb_app_session_start() { radio_recheck_fault; return 1; }
    dxb_radio_claim r1 "$app" > "$TEST_TMP/out" 2> "$TEST_TMP/err"; assert_eq "$?" 5
    if [[ $app == alpha ]]; then assert_eq "$(tail -1 "$TEST_TMP/audio")" r1:0
    else assert_eq "$(tail -1 "$TEST_TMP/rig-kinds")" r1:none:0; fi
    assert_eq "$(jq -r .radios.r1.owner "$DXB_RADIOS_FILE")" ''
  done
)

test_radio_wait_recheck_release_uses_fresh_presence_after_settle() (
  radio_recheck_env; RECHECK_FAULT=replacement
  _dxb_radio_set_owner r1 alpha; _dxb_radio_set_owner r2 alpha
  echo alpha.service > "$TEST_TMP/active"
  dxb_audio_sync() { printf '%s:%s\n' "$1" "$2" >> "$TEST_TMP/audio"; }
  assert_ok dxb_radio_release r1
  assert_eq "$(tail -1 "$TEST_TMP/audio")" r1:0
  assert_eq "$(jq -r .radios.r1.owner "$DXB_RADIOS_FILE")" ''
  assert_eq "$(jq -r .radios.r2.owner "$DXB_RADIOS_FILE")" alpha
)

test_radio_wait_recheck_matching_device_can_renumber_during_start() (
  radio_recheck_env; RECHECK_FAULT=renumber
  assert_ok dxb_radio_claim r1 alpha
  assert_eq "$(jq -r .cat <<< "$(dxb_radio_kernel_names r1)")" ttyUSB9
  assert_contains "$(cat "$TEST_TMP/appcalls")" 'alpha wire r1'
  assert_eq "$(jq -r .radios.r1.owner "$DXB_RADIOS_FILE")" alpha
)
