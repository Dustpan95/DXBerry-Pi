#!/usr/bin/env bash
# Network/package-manager replies are fixtures; no package is installed.
# shellcheck disable=SC1090,SC1091,SC2030,SC2031,SC2034,SC2317

test_update_freshness_failed_refresh_never_reports_zero_or_simulates_old_lists() (
  up_env
  echo update > "$TEST_TMP/apt-fail-on"
  echo 100 > "$TEST_TMP/apt-rc"
  : > "$TEST_TMP/apt-sim"
  local j
  j=$(dxb_update_check refresh)
  assert_eq "$(jq -r '.system.error | type' <<< "$j")" string
  assert_eq "$(jq -c '.system.count // null' <<< "$j")" null
  assert_not_contains "$(cat "$TEST_TMP/calls")" 'apt-get -s'
)

test_update_freshness_partial_repository_failure_requires_strict_refresh() (
  up_env
  up_apt() {
    echo "apt-get $*" >> "$TEST_TMP/calls"
    if [[ $* == *update* && $* == *APT::Update::Error-Mode=any* ]]; then return 100; fi
    if [[ $* == *'-s '* ]]; then echo 'Inst stale [1] (2 Debian:13/stable [arm64])'; fi
    return 0
  }
  local j
  j=$(dxb_update_check refresh)
  assert_eq "$(jq -r '.system.error | type' <<< "$j")" string
  assert_contains "$(cat "$TEST_TMP/calls")" 'APT::Update::Error-Mode=any'
  assert_not_contains "$(cat "$TEST_TMP/calls")" 'apt-get -s'
)

test_update_freshness_debian_programs_are_unknown_but_upstream_checks_continue() (
  up_env; up_apps_env
  AP_INSTALLED='wsjtx=3.0.2 fldigi=4.1.0'; AP_CANDIDATE=4.1.0
  echo update > "$TEST_TMP/apt-fail-on"; echo 100 > "$TEST_TMP/apt-rc"
  local j
  j=$(dxb_update_check refresh)
  assert_eq "$(jq -r '.apps.fldigi.error | type' <<< "$j")" string
  assert_eq "$(jq -c '[.apps.fldigi.latest,.apps.fldigi.update]' <<< "$j")" '[null,null]'
  assert_eq "$(jq -r '.apps.wsjtx.update' <<< "$j")" true
  # A later successful check must not inherit the failed invocation's state.
  rm "$TEST_TMP/apt-fail-on" "$TEST_TMP/apt-rc"
  j=$(dxb_update_check refresh)
  assert_eq "$(jq -c '[.system.count,.apps.fldigi.latest,.apps.fldigi.update]' <<< "$j")" '[0,"4.1.0",false]'
)

test_update_freshness_legacy_or_unknown_cache_is_rechecked() (
  up_env
  local version j now
  now=$(date +%s)
  for version in null 1 3 '"2"'; do
    jq -cn --argjson t "$now" --argjson v "$version" '{version:$v,checked_at:$t,graywolf:{update:false},dxberry:{update:false},system:{count:0}}' > "$DXB_UPDATE_CACHE"
    j=$(dxb_update_check)
    assert_eq "$(jq -r .cached <<< "$j")" false
    assert_eq "$(jq -r .version <<< "$j")" 2
    assert_eq "$(jq -r .system.count <<< "$j")" 2
  done
  j=$(dxb_update_check)
  assert_eq "$(jq -r .cached <<< "$j")" true
)

test_update_freshness_status_does_not_reuse_legacy_success_or_run_a_check() (
  up_env
  source "$DXB_LIB/status.sh"
  DXB_STATION_CMD=$TEST_TMP/missing
  dxb_gw_installed_version() { echo fixture; }
  echo '{"checked_at":123,"graywolf":{"update":false},"dxberry":{"update":false},"system":{"count":0}}' > "$DXB_UPDATE_CACHE"
  local j
  j=$(dxb_status_release)
  assert_eq "$(jq -c .update <<< "$j")" null
  assert_eq "$(cat "$TEST_TMP/calls")" ''
  jq '. + {version:2}' "$DXB_UPDATE_CACHE" > "$TEST_TMP/current"
  mv "$TEST_TMP/current" "$DXB_UPDATE_CACHE"
  j=$(dxb_status_release)
  assert_eq "$(jq -r .update.system <<< "$j")" 0
  echo '{"version":2,"system":{}}' > "$DXB_UPDATE_CACHE"
  j=$(dxb_status_release)
  assert_eq "$(jq -c '[.update.graywolf,.update.dxberry,.update.system]' <<< "$j")" '[null,null,null]'
)
