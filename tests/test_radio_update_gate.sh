#!/usr/bin/env bash
# Manager replies are fixtures; worker/update exclusion uses real kernel flock.
# shellcheck disable=SC1090,SC1091,SC2030,SC2031,SC2034,SC2317

test_radio_update_gate_read_lease_preserves_effects_results_and_refuses_bad_locks() (
  local base=$TEST_TMP fault state=before
  for fault in link hardlink data; do
    TEST_TMP=$base/$fault; mkdir -p "$TEST_TMP/run"; export TEST_TMP DXB_RUN_DIR=$TEST_TMP/run
    case $fault in
      link) : > "$TEST_TMP/target"; ln -s "$TEST_TMP/target" "$DXB_RUN_DIR/update-apply.lock" ;;
      hardlink) (umask 077; : > "$TEST_TMP/target"); ln "$TEST_TMP/target" "$DXB_RUN_DIR/update-apply.lock" ;;
      data) (umask 077; echo invalid > "$DXB_RUN_DIR/update-apply.lock") ;;
    esac
    lease_effect() { state=changed; }
    dxb_with_update_read_lock lease_effect > "$TEST_TMP/out" 2> "$TEST_TMP/err"; assert_eq "$?" 6
    assert_eq "$state" before
  done
  TEST_TMP=$base/good; mkdir -p "$TEST_TMP"; export TEST_TMP DXB_RUN_DIR=$TEST_TMP/run
  lease_effect() { state=changed; return 7; }
  dxb_with_update_read_lock lease_effect; assert_eq "$?" 7
  assert_eq "$state" changed
  assert_ok command flock -x -n "$DXB_RUN_DIR/update-apply.lock" true
  lease_effect() { state=retained; chmod 644 "$DXB_RUN_DIR/update-apply.lock"; }
  dxb_with_update_read_lock lease_effect > "$TEST_TMP/out" 2> "$TEST_TMP/err"; assert_eq "$?" 6
  assert_eq "$state" retained
)

test_radio_update_gate_worker_retains_request_during_an_update() (
  radio_events_env
  assert_ok "$EVENT_CMD" enqueue
  (umask 077; : > "$DXB_RUN_DIR/update-apply.lock")
  exec 7< "$DXB_RUN_DIR/update-apply.lock"; command flock -x 7
  "$EVENT_CMD" reconcile > "$TEST_TMP/out" 2> "$TEST_TMP/err"; assert_eq "$?" 6
  assert_eq "$(cat "$TEST_TMP/attempts")" ''
  [[ -d $EVENT_MARKS/pending ]] || _fail 'update contention consumed the pending request'
  exec 7>&-
  assert_ok "$EVENT_CMD" reconcile
  assert_eq "$(wc -l < "$TEST_TMP/attempts")" 1
)

test_radio_update_gate_child_keeps_lease_after_worker_parent_dies() (
  radio_events_env
  cat > "$DXB_RADIO_EVENT_RADIO_CMD" <<'CHILD'
#!/bin/bash
printf '%s\n' "$$" > "$TEST_TMP/child-pid"
: > "$TEST_TMP/started"
read -r _ < "$TEST_TMP/gate"
CHILD
  mkfifo "$TEST_TMP/gate"
  assert_ok "$EVENT_CMD" enqueue
  "$EVENT_CMD" reconcile > "$TEST_TMP/out" 2> "$TEST_TMP/err" & local pid=$!
  if ! radio_events_wait_started "$pid"; then kill "$pid" 2>/dev/null || true; _fail 'worker did not start its child'; return; fi
  kill -KILL "$pid"
  wait "$pid" 2>/dev/null; assert_eq "$?" 137
  if command flock -x -n "$DXB_RUN_DIR/update-apply.lock" true; then _fail 'surviving child lost the update exclusion lease'; fi
  printf 'continue\n' > "$TEST_TMP/gate"
  local deadline=$((SECONDS+5))
  until command flock -x -n "$DXB_RUN_DIR/update-apply.lock" true; do
    (( SECONDS < deadline )) || { _fail 'child did not release its lease'; return; }
    command sleep .01
  done
  [[ -d $EVENT_MARKS/processing ]] || _fail 'dead parent acknowledged its child work'
)

test_radio_update_gate_blocks_opt_in_unit_and_rule_publication() (
  radio_events_env
  assert_ok "$EVENT_CMD" enqueue
  (umask 077; : > "$DXB_RUN_DIR/update-apply.lock")
  exec 7< "$DXB_RUN_DIR/update-apply.lock"; command flock -x 7
  rig_env
  DXB_RADIO_EVENT_QUEUE=on dxb_rigctld_install_units > "$TEST_TMP/out" 2> "$TEST_TMP/err"; assert_eq "$?" 6
  [[ ! -e $DXB_SYSTEMD_DIR/dxberry-radio-reconcile.path ]] || _fail 'published opt-in units through an update'
  udev_env; dxb_radio_load
  mkdir -p "$TEST_TMP/etc"
  echo retained > "$DXB_UDEV_RULES_FILE"
  DXB_RADIO_EVENT_QUEUE=on dxb_radio_udev_write "$DXB_RADIOS" > "$TEST_TMP/out" 2> "$TEST_TMP/err"; assert_eq "$?" 6
  assert_eq "$(cat "$DXB_UDEV_RULES_FILE")" retained
)

radio_update_tree_env() {
  up_env; up_provision_stub
  up_tree "$DXB_OPT" 0.5.0-rc4
  up_tree "$DXB_OPT.prev" 0.5.0-rc3
  export DXB_SYSTEMD_DIR=$TEST_TMP/systemd DXB_UPDATE_RADIO_RULES_FILE=$TEST_TMP/etc/70-radio.rules
  mkdir -p "$DXB_SYSTEMD_DIR"
}

radio_update_empty_queue() {
  mkdir -p "$DXB_RUN_DIR/radio-events/marks"
  chmod 700 "$DXB_RUN_DIR/radio-events" "$DXB_RUN_DIR/radio-events/marks"
}

test_radio_update_gate_rollback_refuses_configured_units_without_changing_trees() (
  radio_update_tree_env
  echo retained > "$DXB_SYSTEMD_DIR/dxberry-radio-reconcile.path"
  dxb_update_apply_rollback > "$TEST_TMP/out" 2> "$TEST_TMP/err"; assert_eq "$?" 6
  assert_eq "$(cat "$DXB_OPT/VERSION")" 0.5.0-rc4
  assert_eq "$(cat "$DXB_OPT.prev/VERSION")" 0.5.0-rc3
  [[ ! -e $TEST_TMP/provision-ran ]] || _fail 'ran provisioning despite retained queue units'
  assert_eq "$(cat "$DXB_SYSTEMD_DIR/dxberry-radio-reconcile.path")" retained
)

test_radio_update_gate_rollback_refuses_pending_producer_or_history() (
  local base=$TEST_TMP fault
  for fault in pending producer history unknown; do
    TEST_TMP=$base/$fault; mkdir -p "$TEST_TMP"; export TEST_TMP
    radio_update_tree_env
    case $fault in
      pending) radio_update_empty_queue; mkdir -m 700 "$DXB_RUN_DIR/radio-events/marks/pending" ;;
      producer) echo 'RUN+="/opt/dxberry/bin/dxberry-radio-event enqueue"' > "$DXB_UPDATE_RADIO_RULES_FILE" ;;
      history) mkdir -m 700 "$DXB_RUN_DIR/radio-events"; (umask 077; : > "$DXB_RUN_DIR/radio-events/worker.lock") ;;
      unknown) radio_update_empty_queue; : > "$DXB_RUN_DIR/radio-events/marks/unknown" ;;
    esac
    dxb_update_apply_rollback > "$TEST_TMP/out" 2> "$TEST_TMP/err"; assert_eq "$?" 6
    assert_eq "$(cat "$DXB_OPT/VERSION")" 0.5.0-rc4
    [[ ! -e $TEST_TMP/provision-ran ]] || _fail "$fault ran provisioning"
    case $fault in pending) [[ -d $DXB_RUN_DIR/radio-events/marks/pending ]] || _fail 'lost pending work' ;; esac
  done
)

test_radio_update_gate_manager_unknown_or_loaded_is_not_absence() (
  local base=$TEST_TMP fault
  for fault in loaded failed incomplete duplicate job; do
    TEST_TMP=$base/$fault; mkdir -p "$TEST_TMP"; export TEST_TMP
    radio_update_tree_env
    queue_manager() {
      case $fault in
        loaded) printf 'LoadState=loaded\nActiveState=inactive\nJob=\n' ;;
        failed) return 1 ;;
        incomplete) printf 'LoadState=not-found\n' ;;
        duplicate) printf 'LoadState=not-found\nActiveState=inactive\nJob=\nJob=\n' ;;
        job) printf 'LoadState=not-found\nActiveState=inactive\nJob=123\n' ;;
      esac
    }
    DXB_SYSTEMCTL=queue_manager dxb_update_apply_rollback > "$TEST_TMP/out" 2> "$TEST_TMP/err"; assert_eq "$?" 6
    assert_eq "$(cat "$DXB_OPT/VERSION")" 0.5.0-rc4
  done
)

test_radio_update_gate_legacy_absence_and_empty_private_queue_allow_rollback() (
  radio_update_tree_env
  radio_update_empty_queue
  assert_ok dxb_update_apply_rollback > "$TEST_TMP/out" 2> "$TEST_TMP/err"
  assert_eq "$(cat "$DXB_OPT/VERSION")" 0.5.0-rc3
  [[ -d $DXB_RUN_DIR/radio-events/marks ]] || _fail 'removed empty private queue directory'
)

test_radio_update_gate_new_update_refuses_opt_in_before_download() (
  radio_update_tree_env
  DXB_RADIO_EVENT_QUEUE=on dxb_update_apply_dxberry > "$TEST_TMP/out" 2> "$TEST_TMP/err"; assert_eq "$?" 6
  assert_not_contains "$(cat "$TEST_TMP/calls")" curl
  assert_eq "$(cat "$DXB_OPT/VERSION")" 0.5.0-rc4
)

test_radio_update_gate_rechecks_after_download_before_swap() (
  up_env; up_provision_stub
  export DXB_SYSTEMD_DIR=$TEST_TMP/systemd DXB_UPDATE_RADIO_RULES_FILE=$TEST_TMP/etc/70-radio.rules
  mkdir -p "$DXB_SYSTEMD_DIR"
  up_tree "$DXB_OPT" 0.3.0-rc3
  dxb_update_set_prereleases on
  up_release_file 0.3.0-rc4
  late_curl() {
    up_curl "$@" || return $?
    echo retained > "$DXB_SYSTEMD_DIR/dxberry-radio-reconcile.path"
  }
  DXB_UPDATE_CURL=late_curl dxb_update_apply_dxberry > "$TEST_TMP/out" 2> "$TEST_TMP/err"; assert_eq "$?" 6
  assert_eq "$(cat "$DXB_OPT/VERSION")" 0.3.0-rc3
  [[ ! -e $DXB_OPT.prev ]] || _fail 'moved current tree after late queue configuration'
)
