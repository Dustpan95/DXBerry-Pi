#!/usr/bin/env bash
# Protocol responses are fixture programs; positive coverage uses the real helper.
# shellcheck disable=SC1090,SC1091,SC2030,SC2031,SC2034,SC2317

radio_bundle_env() {
  rig_env; udev_env; dxb_radio_load
  export DXB_RADIO_EVENT_PROTOCOL_CMD=$TEST_TMP/bundle/bin/dxberry-radio-event
  mkdir -p "$TEST_TMP/bundle/bin"
  cp "$DXB_ROOT/provision/bin/dxberry-radio-event" "$DXB_RADIO_EVENT_PROTOCOL_CMD"
  chmod 755 "$DXB_RADIO_EVENT_PROTOCOL_CMD"
  echo retained > "$DXB_UDEV_RULES_FILE"
}

radio_bundle_refuses_publication() {
  DXB_RADIO_EVENT_QUEUE=on dxb_rigctld_install_units > "$TEST_TMP/out" 2> "$TEST_TMP/err"; assert_eq "$?" 6
  [[ ! -e $DXB_SYSTEMD_DIR/dxberry-radio-reconcile.path ]] || _fail 'published incompatible queue units'
  assert_eq "$(cat "$TEST_TMP/calls")" ''
  DXB_RADIO_EVENT_QUEUE=on dxb_radio_udev_write "$DXB_RADIOS" > "$TEST_TMP/out" 2> "$TEST_TMP/err"; assert_eq "$?" 6
  assert_eq "$(cat "$DXB_UDEV_RULES_FILE")" retained
  assert_eq "$(cat "$TEST_TMP/calls")" ''
}

test_radio_bundle_incomplete_shared_library_refuses_before_publication() (
  local base=$TEST_TMP missing
  for missing in dxb_with_update_read_lock dxb_radio_event_protocol_ready; do
    (
      TEST_TMP=$base/$missing; mkdir -p "$TEST_TMP"; export TEST_TMP
      radio_bundle_env
      unset -f "$missing"
      radio_bundle_refuses_publication
    )
  done
)

test_radio_bundle_missing_old_failed_and_ambiguous_protocol_refuse_publication() (
  local base=$TEST_TMP fault
  for fault in missing old failed ambiguous; do
    TEST_TMP=$base/$fault; mkdir -p "$TEST_TMP"; export TEST_TMP
    radio_bundle_env
    case $fault in
      missing) rm "$DXB_RADIO_EVENT_PROTOCOL_CMD" ;;
      old) printf '#!/bin/bash\nprintf "1\\n"\n' > "$DXB_RADIO_EVENT_PROTOCOL_CMD" ;;
      failed) printf '#!/bin/bash\nprintf "2\\n"\nexit 7\n' > "$DXB_RADIO_EVENT_PROTOCOL_CMD" ;;
      ambiguous) printf '#!/bin/bash\nprintf "2\\n\\n"\n' > "$DXB_RADIO_EVENT_PROTOCOL_CMD" ;;
    esac
    radio_bundle_refuses_publication
  done
)

test_radio_bundle_unsafe_helper_or_parent_refuses_without_executing() (
  local base=$TEST_TMP fault
  for fault in link hardlink owner mode parent; do
    TEST_TMP=$base/$fault; mkdir -p "$TEST_TMP"; export TEST_TMP
    radio_bundle_env
    cat > "$DXB_RADIO_EVENT_PROTOCOL_CMD" <<'HELPER'
#!/bin/bash
: > "$TEST_TMP/executed"
printf '2\n'
HELPER
    case $fault in
      link) mv "$DXB_RADIO_EVENT_PROTOCOL_CMD" "$TEST_TMP/target"; ln -s "$TEST_TMP/target" "$DXB_RADIO_EVENT_PROTOCOL_CMD" ;;
      hardlink) ln "$DXB_RADIO_EVENT_PROTOCOL_CMD" "$TEST_TMP/alias" ;;
      owner) chown 1000 "$DXB_RADIO_EVENT_PROTOCOL_CMD" ;;
      mode) chmod 777 "$DXB_RADIO_EVENT_PROTOCOL_CMD" ;;
      parent) chmod 777 "$TEST_TMP/bundle/bin" ;;
    esac
    radio_bundle_refuses_publication
    [[ ! -e $TEST_TMP/executed ]] || _fail "$fault helper was executed"
  done
)

test_radio_bundle_oversized_reply_is_bounded_and_refused() (
  radio_bundle_env
  cat > "$DXB_RADIO_EVENT_PROTOCOL_CMD" <<'HELPER'
#!/bin/bash
printf '%4096s' x
HELPER
  radio_bundle_refuses_publication
  assert_ok command flock -x -n "$DXB_RUN_DIR/update-apply.lock" true
)

test_radio_bundle_hung_reply_times_out_and_releases_update_lease() (
  radio_bundle_env
  cat > "$DXB_RADIO_EVENT_PROTOCOL_CMD" <<'HELPER'
#!/bin/bash
printf '%s\n' "$$" > "$TEST_TMP/probe-pid"
read -r _ < "$TEST_TMP/probe-gate"
HELPER
  mkfifo "$TEST_TMP/probe-gate"
  DXB_RADIO_EVENT_QUEUE=on dxb_rigctld_install_units > "$TEST_TMP/out" 2> "$TEST_TMP/err"; assert_eq "$?" 6
  [[ ! -e $DXB_SYSTEMD_DIR/dxberry-radio-reconcile.path ]] || _fail 'hung helper published units'
  local pid; pid=$(cat "$TEST_TMP/probe-pid")
  if kill -0 "$pid" 2>/dev/null; then _fail 'timed-out probe remained running'; kill -KILL "$pid" 2>/dev/null || true; fi
  assert_ok command flock -x -n "$DXB_RUN_DIR/update-apply.lock" true
)

test_radio_bundle_changed_bytes_or_path_during_probe_refuse_publication() (
  local base=$TEST_TMP fault
  for fault in bytes path; do
    TEST_TMP=$base/$fault; mkdir -p "$TEST_TMP"; export TEST_TMP
    radio_bundle_env
    export BUNDLE_FAULT=$fault
    cat > "$DXB_RADIO_EVENT_PROTOCOL_CMD" <<'HELPER'
#!/bin/bash
printf '2\n'
if [[ $BUNDLE_FAULT == bytes ]]; then
  printf '# changed\n' >> "$0"
else
  cp "$0" "$0.new"
  mv "$0.new" "$0"
fi
HELPER
    radio_bundle_refuses_publication
  done
)

test_radio_bundle_real_protocol_allows_explicit_publication_without_queue_work() (
  radio_bundle_env
  DXB_RADIO_EVENT_QUEUE=on assert_ok dxb_rigctld_install_units
  DXB_RADIO_EVENT_QUEUE=on assert_ok dxb_radio_udev_write "$DXB_RADIOS"
  assert_file_contains "$DXB_SYSTEMD_DIR/dxberry-radio-reconcile.path" 'DirectoryNotEmpty='
  assert_file_contains "$DXB_UDEV_RULES_FILE" 'dxberry-radio-event enqueue'
  [[ ! -e $DXB_RUN_DIR/radio-events ]] || _fail 'protocol query initialized or ran queue work'
  # The shared gate and query must agree on the normal runtime-directory default.
  (
    unset DXB_RUN_DIR
    DXB_RADIO_EVENT_QUEUE=on dxb_rigctld_install_units
    assert_eq "$?" 1
  )
)

test_radio_bundle_update_excluded_through_protocol_wait_and_publication() (
  radio_bundle_env
  cat > "$DXB_RADIO_EVENT_PROTOCOL_CMD" <<'HELPER'
#!/bin/bash
: > "$TEST_TMP/started"
read -r _ < "$TEST_TMP/probe-gate"
printf '2\n'
HELPER
  mkfifo "$TEST_TMP/probe-gate"
  DXB_RADIO_EVENT_QUEUE=on dxb_rigctld_install_units > "$TEST_TMP/out" 2> "$TEST_TMP/err" & local pid=$!
  if ! radio_events_wait_started "$pid"; then kill "$pid" 2>/dev/null || true; _fail 'protocol probe did not start'; return; fi
  if command flock -x -n "$DXB_RUN_DIR/update-apply.lock" true; then _fail 'probe wait lost update exclusion'; fi
  printf 'continue\n' > "$TEST_TMP/probe-gate"
  assert_ok wait "$pid"
  assert_file_contains "$DXB_SYSTEMD_DIR/dxberry-radio-reconcile.path" 'DirectoryNotEmpty='
  assert_ok command flock -x -n "$DXB_RUN_DIR/update-apply.lock" true
)

test_radio_bundle_default_off_never_executes_or_requires_a_protocol_probe() (
  radio_bundle_env
  cat > "$DXB_RADIO_EVENT_PROTOCOL_CMD" <<'HELPER'
#!/bin/bash
: > "$TEST_TMP/executed"
exit 7
HELPER
  DXB_RADIO_EVENT_QUEUE=off assert_ok dxb_rigctld_install_units
  DXB_RADIO_EVENT_QUEUE=off assert_ok dxb_radio_udev_write "$DXB_RADIOS"
  [[ ! -e $TEST_TMP/executed ]] || _fail 'legacy publication executed experimental probe'
  [[ ! -e $DXB_SYSTEMD_DIR/dxberry-radio-reconcile.path ]] || _fail 'legacy publication installed queue units'
)
