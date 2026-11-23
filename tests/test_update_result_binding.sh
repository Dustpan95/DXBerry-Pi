#!/usr/bin/env bash
# Each fixture isolates callbacks and observes metadata files only.
# shellcheck disable=SC1090,SC1091,SC2030,SC2031,SC2034,SC2317

update_binding_job() {
  jq -cn --arg id "$1" '{version:2,run_id:$id,unit:("dxberry-job-update-system-"+$id),kind:"system",name:null,started:1}'
}

update_binding_result() {
  jq -cn --arg id "$1" '{version:2,run_id:$id,unit:("dxberry-job-update-system-"+$id),exit:0,finished:2}'
}

update_binding_gate() { (umask 077; : > "$DXB_RUN_DIR/update-apply.lock"); }

test_update_result_binding_rejects_unrelated_and_legacy_results() (
  up_cli_env
  update_binding_gate
  local current=11111111111111111111111111111111 other=22222222222222222222222222222222 value
  update_binding_job "$current" > "$DXB_UPDATE_JOB_FILE"
  for value in "$(update_binding_result "$other")" '{"exit":0,"finished":2}' 'garbage' \
    '{"version":2,"run_id":"11111111111111111111111111111111","unit":"dxberry-job-update-system-11111111111111111111111111111111","exit":"0","finished":2}'; do
    printf '%s\n' "$value" > "$DXB_UPDATE_RESULT"
    assert_ok up_cli job --json
    assert_eq "$(jq -c '[.job.state,.job.exit,.job.finished]' "$TEST_TMP/out")" '["ended",null,null]'
  done
  update_binding_result "$current" > "$DXB_UPDATE_RESULT"
  assert_ok up_cli job --json
  assert_eq "$(jq -c '[.job.state,.job.exit,.job.status]' "$TEST_TMP/out")" '["finished",0,"done"]'
  jq 'del(.version,.run_id)' "$DXB_UPDATE_JOB_FILE" > "$TEST_TMP/job"
  mv "$TEST_TMP/job" "$DXB_UPDATE_JOB_FILE"
  assert_ok up_cli job --json
  assert_eq "$(jq -c '[.job.state,.job.exit]' "$TEST_TMP/out")" '["ended",null]'
)

test_update_result_binding_worker_echoes_identity_and_refuses_bad_metadata() (
  up_cli_env
  local id=11111111111111111111111111111111 unit=dxberry-job-update-system-11111111111111111111111111111111 fault
  (
    source "$DXB_ROOT/provision/bin/dxberry-update"
    dxb_require_root() { :; }
    dxb_update_apply_system() { : > "$TEST_TMP/body"; return 8; }
    DXB_UPDATE_RUN_ID=$id DXB_UPDATE_RUN_UNIT=$unit main apply system
  ) > /dev/null 2>&1
  assert_eq "$?" 8
  assert_eq "$(jq -c '[.version,.run_id,.unit,.exit]' "$DXB_UPDATE_RESULT")" "[2,\"$id\",\"$unit\",8]"
  rm -f "$TEST_TMP/body"
  for fault in missing-unit bad-id wrong-unit; do
    (
      source "$DXB_ROOT/provision/bin/dxberry-update"
      dxb_require_root() { :; }
      dxb_update_apply_system() { : > "$TEST_TMP/body"; return 8; }
      export DXB_UPDATE_RUN_ID=$id DXB_UPDATE_RUN_UNIT=$unit
      case $fault in missing-unit) unset DXB_UPDATE_RUN_UNIT ;; bad-id) DXB_UPDATE_RUN_ID=invalid ;; wrong-unit) DXB_UPDATE_RUN_UNIT=dxberry-job-update-system-other ;; esac
      main apply system
    ) > /dev/null 2>&1
    assert_eq "$?" 6
    [[ ! -e $TEST_TMP/body ]] || _fail "$fault entered worker body"
    assert_eq "$(jq -r .run_id "$DXB_UPDATE_RESULT")" "$id"
  done
  update_binding_job "$id" > "$DXB_UPDATE_JOB_FILE"
  update_binding_result 22222222222222222222222222222222 > "$DXB_UPDATE_RESULT"
  (
    source "$DXB_ROOT/provision/bin/dxberry-update"
    dxb_require_root() { :; }
    dxb_update_apply_system() { return 0; }
    mv() { [[ ${*: -1} != "$DXB_UPDATE_RESULT" ]] || return 1; command mv "$@"; }
    DXB_UPDATE_RUN_ID=$id DXB_UPDATE_RUN_UNIT=$unit main apply system
  ) > /dev/null 2>&1
  assert_eq "$?" 8
  assert_ok up_cli job --json
  assert_eq "$(jq -c '[.job.state,.job.exit]' "$TEST_TMP/out")" '["ended",null]'
)

test_update_result_binding_start_publishes_before_fast_worker_and_uses_unique_units() (
  up_cli_env
  update_binding_gate
  local first second
  (
    source "$DXB_ROOT/provision/bin/dxberry-update"
    dxb_require_root() { :; }
    date() { echo 1000; }
    up_systemd_run() {
      local id='' unit='' arg
      for arg in "$@"; do case $arg in --setenv=DXB_UPDATE_RUN_ID=*) id=${arg#--setenv=DXB_UPDATE_RUN_ID=} ;; --unit=*) unit=${arg#--unit=} ;; esac; done
      [[ $id =~ ^[0-9a-f]{32}$ && $(jq -r .run_id "$DXB_UPDATE_JOB_FILE") == "$id" && $(jq -r .unit "$DXB_UPDATE_JOB_FILE") == "$unit" ]] \
        || { _fail 'job identity not published before launch'; return 1; }
      update_binding_result "$id" > "$DXB_UPDATE_RESULT"
    }
    main system --json
    main system --json
  ) > "$TEST_TMP/starts" 2> "$TEST_TMP/err"
  assert_eq "$?" 0
  first=$(head -1 "$TEST_TMP/starts" | jq -r .job)
  second=$(tail -1 "$TEST_TMP/starts" | jq -r .job)
  [[ -n $first && $first != "$second" ]] || _fail 'same-clock jobs reused an identity'
  assert_eq "$(jq -r .version "$DXB_UPDATE_JOB_FILE")" 2
  assert_ok up_cli job --json
  assert_eq "$(jq -c '[.job.state,.job.exit]' "$TEST_TMP/out")" '["finished",0]'
  assert_eq "$(jq -c '[.job | has("run_id"),has("version")]' "$TEST_TMP/out")" '[false,false]'
)

test_update_result_binding_refuses_entropy_and_publication_failure_before_launch() (
  local base=$TEST_TMP fault
  for fault in entropy zero repeated publication; do
    TEST_TMP=$base/$fault; export TEST_TMP
    up_cli_env
    update_binding_job 11111111111111111111111111111111 > "$DXB_UPDATE_JOB_FILE"
    update_binding_result 11111111111111111111111111111111 > "$DXB_UPDATE_RESULT"
    (
      source "$DXB_ROOT/provision/bin/dxberry-update"
      dxb_require_root() { :; }
      up_systemd_run() { : > "$TEST_TMP/launched"; }
      case $fault in
        entropy) _dxb_update_new_run_id() { return 1; } ;;
        zero) _dxb_update_new_run_id() { echo 00000000000000000000000000000000; } ;;
        repeated) _dxb_update_new_run_id() { echo 11111111111111111111111111111111; } ;;
        publication) _dxb_update_write_record() { return 6; } ;;
      esac
      main system --json
    ) > "$TEST_TMP/out" 2> "$TEST_TMP/err"
    assert_eq "$?" 6
    [[ ! -e $TEST_TMP/launched ]] || _fail "$fault launched untracked work"
    assert_eq "$(jq -r .run_id "$DXB_UPDATE_RESULT")" 11111111111111111111111111111111
  done
)

test_update_result_binding_changed_job_during_read_never_inherits_old_unit_state() (
  up_cli_env
  update_binding_job 11111111111111111111111111111111 > "$DXB_UPDATE_JOB_FILE"
  (
    source "$DXB_ROOT/provision/bin/dxberry-update"
    dxb_require_root() { :; }
    systemctl() {
      update_binding_job 22222222222222222222222222222222 > "$DXB_UPDATE_JOB_FILE"
      return 0
    }
    main job --json
  ) > "$TEST_TMP/out" 2> "$TEST_TMP/err"
  assert_eq "$?" 0
  assert_eq "$(jq -c .job "$TEST_TMP/out")" null
)

test_update_result_binding_failed_launch_cannot_inherit_previous_success() (
  up_cli_env
  update_binding_job 11111111111111111111111111111111 > "$DXB_UPDATE_JOB_FILE"
  update_binding_result 11111111111111111111111111111111 > "$DXB_UPDATE_RESULT"
  : > "$TEST_TMP/systemd-run-fails"
  up_cli system --json; assert_eq "$?" 6
  [[ $(jq -r .run_id "$DXB_UPDATE_JOB_FILE") != 11111111111111111111111111111111 ]] || _fail 'failed request retained previous job identity'
  assert_ok up_cli job --json
  assert_eq "$(jq -c '[.job.state,.job.exit]' "$TEST_TMP/out")" '["ended",null]'
)

test_update_result_binding_held_gate_prevents_premature_completion() (
  up_cli_env
  update_binding_gate
  update_binding_job 11111111111111111111111111111111 > "$DXB_UPDATE_JOB_FILE"
  update_binding_result 11111111111111111111111111111111 > "$DXB_UPDATE_RESULT"
  local holder
  exec {holder}< "$DXB_RUN_DIR/update-apply.lock"
  command flock -x "$holder"
  assert_ok up_cli job --json
  assert_eq "$(jq -c '[.job.state,.job.exit]' "$TEST_TMP/out")" '["ended",null]'
  exec {holder}>&-
  assert_ok up_cli job --json
  assert_eq "$(jq -c '[.job.state,.job.exit]' "$TEST_TMP/out")" '["finished",0]'
)
