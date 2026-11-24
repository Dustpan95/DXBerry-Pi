#!/usr/bin/env bash
# Package-manager replies are fixtures; no package is installed.
# shellcheck disable=SC2030,SC2031,SC2317

test_update_worker_refuses_partial_repository_failure_before_simulation_or_upgrade() (
  up_env
  up_apt() {
    echo "apt-get $*" >> "$TEST_TMP/calls"
    if [[ " $* " == *' update '* && $* == *APT::Update::Error-Mode=any* ]]; then return 100; fi
    if [[ " $* " == *' -s '* ]]; then cat "$TEST_TMP/apt-sim"; fi
    return 0
  }
  dxb_update_apply_system > "$TEST_TMP/out" 2>&1
  assert_eq "$?" 6
  assert_contains "$(cat "$TEST_TMP/out")" 'apt-get update failed'
  assert_not_contains "$(cat "$TEST_TMP/calls")" 'apt-get -s'
  assert_not_contains "$(cat "$TEST_TMP/calls")" 'apt-get -y'
  [[ ! -e $DXB_REBOOT_FLAG ]] || _fail 'failed preparation wrote a reboot flag'
)

test_update_worker_refuses_failed_simulation_even_with_partial_package_output() (
  up_env
  up_apt() {
    echo "apt-get $*" >> "$TEST_TMP/calls"
    if [[ " $* " == *' -s '* ]]; then
      printf 'Inst linux-image-fixture [1] (2 Debian:13/stable [arm64])\n'
      return 100
    fi
    return 0
  }
  _dxb_update_restart_upgraded_programs() { echo restart >> "$TEST_TMP/calls"; }
  # The worker must handle failure itself, independent of its caller's pipefail.
  set +o pipefail
  dxb_update_apply_system > "$TEST_TMP/out" 2>&1
  assert_eq "$?" 6
  assert_contains "$(cat "$TEST_TMP/out")" 'apt-get could not simulate an upgrade'
  assert_not_contains "$(cat "$TEST_TMP/calls")" 'apt-get -y'
  assert_not_contains "$(cat "$TEST_TMP/calls")" restart
  [[ ! -e $DXB_REBOOT_FLAG ]] || _fail 'failed simulation wrote a reboot flag'
)
