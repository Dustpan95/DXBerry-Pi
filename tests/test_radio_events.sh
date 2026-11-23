#!/usr/bin/env bash
# Real processes, kernel flock and private marker directories; apply is a fixture.
# shellcheck disable=SC2030,SC2031,SC2034,SC2317

radio_events_env() {
  export DXB_RUN_DIR=$TEST_TMP/run DXB_RADIO_EVENT_RADIO_CMD=$TEST_TMP/apply \
    DXB_RADIO_EVENT_RETRY_DELAY=0
  EVENT_CMD=$DXB_ROOT/provision/bin/dxberry-radio-event
  EVENT_MARKS=$DXB_RUN_DIR/radio-events/marks
  : > "$TEST_TMP/attempts"
  cat > "$DXB_RADIO_EVENT_RADIO_CMD" <<'APPLY'
#!/bin/bash
[[ $# == 1 && $1 == hotplug ]] || exit 90
for fd in /proc/self/fd/*; do
  [[ $(readlink "$fd" 2>/dev/null) != "$DXB_RUN_DIR/radio-events/worker.lock" ]] || exit 91
done
if flock -n "$DXB_RUN_DIR/radio-events/worker.lock" true; then exit 92; fi
echo attempt >> "$TEST_TMP/attempts"
[[ ! -e $TEST_TMP/fail ]] || exit 7
if [[ -p $TEST_TMP/gate ]]; then
  : > "$TEST_TMP/started"
  read -r _ < "$TEST_TMP/gate"
fi
exit 0
APPLY
  chmod 755 "$DXB_RADIO_EVENT_RADIO_CMD"
}

radio_events_wait_started() {
  local pid=$1 i
  for ((i=0;i<300;i++)); do
    [[ ! -e $TEST_TMP/started ]] || return 0
    kill -0 "$pid" 2>/dev/null || return 1
    command sleep .01
  done
  return 1
}

test_radio_events_producer_does_not_wait_for_record_apply_or_worker_locks() (
  radio_events_env
  assert_ok "$EVENT_CMD" enqueue
  exec 8> "$DXB_RUN_DIR/record.lock"; flock 8
  exec 9> "$DXB_RUN_DIR/apply.lock"; flock 9
  (umask 077; : > "$DXB_RUN_DIR/radio-events/worker.lock")
  exec 7< "$DXB_RUN_DIR/radio-events/worker.lock"; flock 7
  assert_ok command timeout 2 "$EVENT_CMD" enqueue
  [[ -d $EVENT_MARKS/pending ]] || _fail 'producer dropped the request while gates were held'
  assert_eq "$(cat "$TEST_TMP/attempts")" ''
)

test_radio_events_coalesces_bursts_and_does_nothing_when_empty() (
  radio_events_env
  local i pid pids=()
  for ((i=0;i<12;i++)); do "$EVENT_CMD" enqueue & pids+=("$!"); done
  for pid in "${pids[@]}"; do assert_ok wait "$pid"; done
  assert_eq "$(ls -A "$EVENT_MARKS")" pending
  assert_ok "$EVENT_CMD" reconcile
  assert_eq "$(wc -l < "$TEST_TMP/attempts")" 1
  assert_eq "$(ls -A "$EVENT_MARKS")" ''
  assert_ok "$EVENT_CMD" reconcile
  assert_eq "$(wc -l < "$TEST_TMP/attempts")" 1
)

test_radio_events_arrival_during_apply_is_not_acknowledged_by_that_apply() (
  radio_events_env
  assert_ok "$EVENT_CMD" enqueue
  mkfifo "$TEST_TMP/gate"
  "$EVENT_CMD" reconcile > "$TEST_TMP/out" 2> "$TEST_TMP/err" & local pid=$!
  if ! radio_events_wait_started "$pid"; then kill "$pid" 2>/dev/null || true; _fail 'worker did not reach apply'; return; fi
  assert_ok "$EVENT_CMD" enqueue
  [[ -d $EVENT_MARKS/pending && -d $EVENT_MARKS/processing ]] || _fail 'arrival did not remain separate from claimed work'
  printf 'continue\n' > "$TEST_TMP/gate"
  assert_ok wait "$pid"
  [[ -d $EVENT_MARKS/pending && ! -e $EVENT_MARKS/processing ]] || _fail 'worker acknowledged a later arrival'
  rm "$TEST_TMP/gate"
  assert_ok "$EVENT_CMD" reconcile
  assert_eq "$(wc -l < "$TEST_TMP/attempts")" 2
  assert_eq "$(ls -A "$EVENT_MARKS")" ''
)

test_radio_events_failed_apply_retains_claimed_and_later_pending_work() (
  radio_events_env
  assert_ok "$EVENT_CMD" enqueue
  : > "$TEST_TMP/fail"
  "$EVENT_CMD" reconcile > "$TEST_TMP/out" 2> "$TEST_TMP/err"; assert_eq "$?" 7
  [[ -d $EVENT_MARKS/processing ]] || _fail 'failed apply erased its claimed work'
  assert_ok "$EVENT_CMD" enqueue
  rm "$TEST_TMP/fail"
  assert_ok "$EVENT_CMD" reconcile
  [[ -d $EVENT_MARKS/pending && ! -e $EVENT_MARKS/processing ]] || _fail 'retry erased a later event'
  assert_ok "$EVENT_CMD" reconcile
  assert_eq "$(wc -l < "$TEST_TMP/attempts")" 3
  assert_eq "$(ls -A "$EVENT_MARKS")" ''
)

test_radio_events_killed_worker_leaves_processing_for_retry() (
  radio_events_env
  assert_ok "$EVENT_CMD" enqueue
  mkfifo "$TEST_TMP/gate"
  setsid "$EVENT_CMD" reconcile > "$TEST_TMP/out" 2> "$TEST_TMP/err" & local pid=$!
  if ! radio_events_wait_started "$pid"; then kill "$pid" 2>/dev/null || true; _fail 'worker did not reach apply'; return; fi
  local pgid
  pgid=$(command ps -o pgid= -p "$pid" | tr -d ' ')
  if [[ $pgid != "$pid" ]]; then kill "$pid" 2>/dev/null || true; _fail 'fixture did not create its own process group'; return; fi
  kill -KILL -- "-$pid"
  wait "$pid" 2>/dev/null; assert_eq "$?" 137
  [[ -d $EVENT_MARKS/processing ]] || _fail 'interrupted work was acknowledged'
  rm "$TEST_TMP/gate"
  assert_ok "$EVENT_CMD" reconcile
  assert_eq "$(ls -A "$EVENT_MARKS")" ''
)

test_radio_events_busy_worker_retains_pending_without_applying() (
  radio_events_env
  assert_ok "$EVENT_CMD" enqueue
  (umask 077; : > "$DXB_RUN_DIR/radio-events/worker.lock")
  exec 7< "$DXB_RUN_DIR/radio-events/worker.lock"; flock 7
  "$EVENT_CMD" reconcile > "$TEST_TMP/out" 2> "$TEST_TMP/err"; assert_eq "$?" 6
  [[ -d $EVENT_MARKS/pending ]] || _fail 'busy worker dropped pending work'
  assert_eq "$(cat "$TEST_TMP/attempts")" ''
)

test_radio_events_rejects_unsafe_queue_state_without_acknowledgement() (
  local base=$TEST_TMP fault
  for fault in queue-link pending-link marker-data unknown-mark lock-link lock-hardlink lock-data; do
    TEST_TMP=$base/$fault; mkdir -p "$TEST_TMP"; export TEST_TMP
    radio_events_env
    assert_ok "$EVENT_CMD" enqueue
    case $fault in
      queue-link) mv "$DXB_RUN_DIR/radio-events" "$TEST_TMP/original"; ln -s "$TEST_TMP/original" "$DXB_RUN_DIR/radio-events" ;;
      pending-link) rmdir "$EVENT_MARKS/pending"; mkdir "$TEST_TMP/target"; ln -s "$TEST_TMP/target" "$EVENT_MARKS/pending" ;;
      marker-data) : > "$EVENT_MARKS/pending/data" ;;
      unknown-mark) : > "$EVENT_MARKS/unknown" ;;
      lock-link) : > "$TEST_TMP/target"; ln -s "$TEST_TMP/target" "$DXB_RUN_DIR/radio-events/worker.lock" ;;
      lock-hardlink) (umask 077; : > "$TEST_TMP/target"); ln "$TEST_TMP/target" "$DXB_RUN_DIR/radio-events/worker.lock" ;;
      lock-data) (umask 077; echo unexpected > "$DXB_RUN_DIR/radio-events/worker.lock") ;;
    esac
    "$EVENT_CMD" reconcile > "$TEST_TMP/out" 2> "$TEST_TMP/err"; assert_eq "$?" 6
    assert_eq "$(cat "$TEST_TMP/attempts")" ''
    [[ -e $EVENT_MARKS/pending || -L $EVENT_MARKS/pending ]] || _fail "$fault erased the retained request"
  done
)

test_radio_events_units_preserve_boot_order_and_add_a_retry_backstop() {
  local path=$DXB_ROOT/provision/templates/dxberry-radio-reconcile.path
  local service=$DXB_ROOT/provision/templates/dxberry-radio-reconcile.service
  local timer=$DXB_ROOT/provision/templates/dxberry-radio-reconcile.timer
  assert_file_contains "$path" 'DirectoryNotEmpty=/run/dxberry/radio-events/marks'
  assert_file_contains "$service" 'After=dxberry-radio-hotplug.service graywolf.service'
  assert_file_contains "$service" 'KillMode=control-group'
  assert_file_contains "$service" 'StartLimitBurst=6'
  assert_file_contains "$timer" 'OnCalendar=*-*-* *:*:00,30'
  assert_file_contains "$timer" 'OnClockChange=yes'
  assert_file_contains "$DXB_ROOT/provision/templates/dxberry-radio-hotplug.service" 'Before=graywolf.service'
  rig_env
  mkdir -p "$TEST_TMP/protocol-bin"
  cp "$DXB_ROOT/provision/bin/dxberry-radio-event" "$TEST_TMP/protocol-bin/dxberry-radio-event"
  chmod 755 "$TEST_TMP/protocol-bin/dxberry-radio-event"
  DXB_RADIO_EVENT_QUEUE=on DXB_RADIO_EVENT_PROTOCOL_CMD=$TEST_TMP/protocol-bin/dxberry-radio-event assert_ok dxb_rigctld_install_units
  assert_file_contains "$DXB_SYSTEMD_DIR/dxberry-radio-reconcile.path" 'DirectoryNotEmpty='
  assert_contains "$(cat "$TEST_TMP/calls")" 'systemctl enable --now dxberry-radio-reconcile.path'
  assert_contains "$(cat "$TEST_TMP/calls")" 'systemctl enable --now dxberry-radio-reconcile.timer'
}

test_radio_events_activation_is_off_by_default_and_requires_explicit_opt_in() (
  rig_env
  unset DXB_RADIO_EVENT_QUEUE
  dxb_rigctld_install_units
  assert_eq "$?" 0
  assert_not_contains "$(cat "$TEST_TMP/calls")" 'enable --now dxberry-radio-reconcile'
  [[ ! -e $DXB_SYSTEMD_DIR/dxberry-radio-reconcile.path ]] || _fail 'default provisioning installed experimental manager units'
  udev_env; dxb_radio_load
  local rules
  rules=$(dxb_radio_udev_rules "$DXB_RADIOS")
  assert_contains "$rules" '/bin/systemctl --no-block start dxberry-radio-hotplug.service'
  assert_not_contains "$rules" 'dxberry-radio-event enqueue'
  rules=$(DXB_RADIO_EVENT_QUEUE=on dxb_radio_udev_rules "$DXB_RADIOS")
  assert_contains "$rules" 'dxberry-radio-event enqueue'
  assert_not_contains "$rules" 'systemctl --no-block start dxberry-radio-hotplug.service'
)
