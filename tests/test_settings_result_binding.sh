#!/usr/bin/env bash
# Each fixture isolates callbacks and observes temporary files only.
# shellcheck disable=SC1090,SC1091,SC2030,SC2031,SC2034,SC2317

settings_binding_job() {
  jq -cn --arg id "$1" '{version:2,run_id:$id,unit:("dxberry-job-config-"+$id),started:1,network:false,changed:["HOSTNAME"]}'
}

settings_binding_result() {
  jq -cn --arg id "$1" '{version:2,run_id:$id,unit:("dxberry-job-config-"+$id),exit:0,finished:2}'
}

settings_binding_gate() { (umask 077; : > "$DXB_RUN_DIR/config-apply.lock"); }

test_settings_result_binding_rejects_unrelated_legacy_and_malformed_results() (
  se_cli_env
  settings_binding_gate
  local current=11111111111111111111111111111111 other=22222222222222222222222222222222 value
  settings_binding_job "$current" > "$DXB_CONFIG_JOB_FILE"
  for value in "$(settings_binding_result "$other")" '{"exit":0,"finished":2}' 'garbage' \
    "$(settings_binding_result "$current" | jq '.exit="0"')" \
    "$(settings_binding_result "$current" | jq '.finished=-1')" \
    "$(settings_binding_result "$current"; settings_binding_result "$current")"; do
    printf '%s\n' "$value" > "$DXB_CONFIG_RESULT"
    assert_ok se_cli job --json
    assert_eq "$(jq -c '[.job.state,.job.exit,.job.finished]' "$TEST_TMP/out")" '["ended",null,null]'
  done
  settings_binding_result "$current" > "$DXB_CONFIG_RESULT"
  assert_ok se_cli job --json
  assert_eq "$(jq -c '[.job.state,.job.exit,.job.status]' "$TEST_TMP/out")" '["finished",0,"done"]'
  assert_eq "$(jq -c '[.job | has("version"),has("run_id")]' "$TEST_TMP/out")" '[false,false]'
  jq 'del(.version,.run_id)' "$DXB_CONFIG_JOB_FILE" > "$TEST_TMP/legacy"
  mv "$TEST_TMP/legacy" "$DXB_CONFIG_JOB_FILE"
  assert_ok se_cli job --json
  assert_eq "$(jq -c '[.job.state,.job.exit]' "$TEST_TMP/out")" '["ended",null]'
  echo '{broken' > "$DXB_CONFIG_JOB_FILE"
  assert_ok se_cli job --json
  assert_eq "$(jq -c .job "$TEST_TMP/out")" null
)

test_settings_result_binding_worker_echoes_identity_and_refuses_bad_metadata() (
  se_cli_env
  local id=11111111111111111111111111111111 unit=dxberry-job-config-11111111111111111111111111111111 fault
  echo HOSTNAME > "$DXB_CONFIG_PENDING"
  echo 1 > "$TEST_TMP/provision-rc"
  DXB_CONFIG_RUN_ID=$id DXB_CONFIG_RUN_UNIT=$unit se_cli apply
  assert_eq "$?" 8
  assert_eq "$(jq -c '[.version,.run_id,.unit,.exit]' "$DXB_CONFIG_RESULT")" "[2,\"$id\",\"$unit\",8]"
  for fault in missing-unit missing-id bad-id zero-id wrong-unit; do
    echo HOSTNAME > "$DXB_CONFIG_PENDING"
    : > "$TEST_TMP/calls"
    (
      export DXB_CONFIG_RUN_ID=$id DXB_CONFIG_RUN_UNIT=$unit
      case $fault in
        missing-unit) unset DXB_CONFIG_RUN_UNIT ;; missing-id) unset DXB_CONFIG_RUN_ID ;;
        bad-id) DXB_CONFIG_RUN_ID=invalid ;; zero-id) DXB_CONFIG_RUN_ID=00000000000000000000000000000000 ;;
        wrong-unit) DXB_CONFIG_RUN_UNIT=dxberry-job-config-other ;;
      esac
      se_cli apply
    )
    assert_eq "$?" 6
    assert_eq "$(cat "$DXB_CONFIG_PENDING")" HOSTNAME
    assert_not_contains "$(cat "$TEST_TMP/calls")" provision
    assert_eq "$(jq -r .run_id "$DXB_CONFIG_RESULT")" "$id"
  done
  echo 0 > "$TEST_TMP/provision-rc"
  assert_ok se_cli apply
  assert_eq "$(jq -c '[.run_id,.unit,.exit]' "$DXB_CONFIG_RESULT")" '[null,null,0]'
)

test_settings_result_binding_publishes_before_fast_worker_with_unique_units() (
  se_cli_env
  local first second
  (
    source "$DXB_ROOT/provision/bin/dxberry-config"
    dxb_require_root() { :; }
    date() { echo 1000; }
    se_systemd_run() {
      local id='' unit='' arg
      for arg in "$@"; do case $arg in --setenv=DXB_CONFIG_RUN_ID=*) id=${arg#--setenv=DXB_CONFIG_RUN_ID=} ;; --unit=*) unit=${arg#--unit=} ;; esac; done
      [[ $id =~ ^[0-9a-f]{32}$ && $(jq -r .run_id "$DXB_CONFIG_JOB_FILE") == "$id" && $(jq -r .unit "$DXB_CONFIG_JOB_FILE") == "$unit" ]] \
        || { _fail 'settings request not published before launch'; return 1; }
      DXB_CONFIG_RUN_ID=$id DXB_CONFIG_RUN_UNIT=$unit cmd_apply > /dev/null
    }
    main set HOSTNAME=first --json
    main set HOSTNAME=second --json
  ) > "$TEST_TMP/starts" 2> "$TEST_TMP/err"
  assert_eq "$?" 0
  first=$(head -1 "$TEST_TMP/starts" | jq -r .job)
  second=$(tail -1 "$TEST_TMP/starts" | jq -r .job)
  [[ -n $first && $first != null && $first != "$second" ]] || _fail 'same-clock settings requests reused an identity'
  assert_ok se_cli job --json
  assert_eq "$(jq -c '[.job.state,.job.exit]' "$TEST_TMP/out")" '["finished",0]'
)

test_settings_result_binding_refuses_entropy_and_rolls_back_failed_publication() (
  local base=$TEST_TMP fault before
  for fault in entropy zero repeated publication network-publication; do
    TEST_TMP=$base/$fault; export TEST_TMP
    se_cli_env
    before=$(se_file)
    settings_binding_job 11111111111111111111111111111111 > "$DXB_CONFIG_JOB_FILE"
    settings_binding_result 11111111111111111111111111111111 > "$DXB_CONFIG_RESULT"
    (
      source "$DXB_ROOT/provision/bin/dxberry-config"
      dxb_require_root() { :; }
      se_systemd_run() {
        if [[ $* == *' apply'* ]]; then : > "$TEST_TMP/launched"; fi
        return 0
      }
      case $fault in
        entropy) _dxb_settings_new_run_id() { return 1; } ;;
        zero) _dxb_settings_new_run_id() { echo 00000000000000000000000000000000; } ;;
        repeated) _dxb_settings_new_run_id() { echo 11111111111111111111111111111111; } ;;
        *publication) _dxb_settings_write_record() { return 6; } ;;
      esac
      if [[ $fault == network-publication ]]; then main set WIFI_SSID=Never --json; else main set HOSTNAME=never --json; fi
    ) > "$TEST_TMP/out" 2> "$TEST_TMP/err"
    assert_eq "$?" 6
    [[ ! -e $TEST_TMP/launched ]] || _fail "$fault launched work"
    assert_eq "$(se_file)" "$before"
    assert_fails dxb_netsafe_pending
    [[ ! -e $DXB_CONFIG_PENDING ]] || _fail "$fault retained pending keys"
    assert_eq "$(jq -r .run_id "$DXB_CONFIG_RESULT")" 11111111111111111111111111111111
  done
)

test_settings_result_binding_changed_job_during_read_is_not_old_unit_evidence() (
  se_cli_env
  settings_binding_job 11111111111111111111111111111111 > "$DXB_CONFIG_JOB_FILE"
  (
    source "$DXB_ROOT/provision/bin/dxberry-config"
    dxb_require_root() { :; }
    systemctl() { settings_binding_job 22222222222222222222222222222222 > "$DXB_CONFIG_JOB_FILE"; return 0; }
    journalctl() { :; }
    main job --json
  ) > "$TEST_TMP/out" 2> "$TEST_TMP/err"
  assert_eq "$?" 0
  assert_eq "$(jq -c .job "$TEST_TMP/out")" null
)

test_settings_result_binding_failed_launch_and_result_publication_do_not_inherit_success() (
  se_cli_env
  settings_binding_gate
  settings_binding_job 11111111111111111111111111111111 > "$DXB_CONFIG_JOB_FILE"
  settings_binding_result 11111111111111111111111111111111 > "$DXB_CONFIG_RESULT"
  : > "$TEST_TMP/job-start-fails"
  se_cli set HOSTNAME=never --json; assert_eq "$?" 6
  [[ $(jq -r .run_id "$DXB_CONFIG_JOB_FILE") != 11111111111111111111111111111111 ]] || _fail 'failed launch retained previous request'
  assert_ok se_cli job --json
  assert_eq "$(jq -c '[.job.state,.job.exit]' "$TEST_TMP/out")" '["ended",null]'
  settings_binding_job 11111111111111111111111111111111 > "$DXB_CONFIG_JOB_FILE"
  settings_binding_result 22222222222222222222222222222222 > "$DXB_CONFIG_RESULT"
  (
    source "$DXB_ROOT/provision/bin/dxberry-config"
    dxb_require_root() { :; }
    mv() { [[ ${*: -1} != "$DXB_CONFIG_RESULT" ]] || return 1; command mv "$@"; }
    DXB_CONFIG_RUN_ID=11111111111111111111111111111111 DXB_CONFIG_RUN_UNIT=dxberry-job-config-11111111111111111111111111111111 main apply
  ) > /dev/null 2>&1
  assert_eq "$?" 8
  assert_ok se_cli job --json
  assert_eq "$(jq -c '[.job.state,.job.exit]' "$TEST_TMP/out")" '["ended",null]'
)

test_settings_result_binding_held_or_unsafe_gate_prevents_completion() (
  se_cli_env
  settings_binding_gate
  settings_binding_job 11111111111111111111111111111111 > "$DXB_CONFIG_JOB_FILE"
  settings_binding_result 11111111111111111111111111111111 > "$DXB_CONFIG_RESULT"
  local holder
  exec {holder}< "$DXB_RUN_DIR/config-apply.lock"
  command flock -x "$holder"
  assert_ok se_cli job --json
  assert_eq "$(jq -c '[.job.state,.job.exit]' "$TEST_TMP/out")" '["ended",null]'
  exec {holder}>&-
  assert_ok se_cli job --json
  assert_eq "$(jq -c '[.job.state,.job.exit]' "$TEST_TMP/out")" '["finished",0]'
  chmod 644 "$DXB_RUN_DIR/config-apply.lock"
  assert_ok se_cli job --json
  assert_eq "$(jq -c '[.job.state,.job.exit]' "$TEST_TMP/out")" '["ended",null]'
)
