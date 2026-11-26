#!/bin/bash
# Boot identities and monotonic intervals, without sound devices or raw log output.
# shellcheck source=provision/lib/capture-diagnostics.sh
source "$DXB_LIB/capture-diagnostics.sh"

cap_env() {
  export DXB_PROC=$TEST_TMP/proc DXB_CAPTURE_JOURNALCTL=$TEST_TMP/journalctl
  unset DXB_CAPTURE_TIMEOUT
  mkdir -p "$DXB_PROC/sys/kernel/random" "$DXB_PROC/asound/card0/pcm0c/sub0" "$DXB_PROC/17"
  echo 11111111-1111-1111-1111-111111111111 > "$DXB_PROC/sys/kernel/random/boot_id"
  printf 'state: RUNNING\nowner_pid   : 17\n' > "$DXB_PROC/asound/card0/pcm0c/sub0/status"
  printf 'format: S16_LE\nrate: 48000 (48000/1)\nperiod_size: 241\nbuffer_size: 482\n' > "$DXB_PROC/asound/card0/pcm0c/sub0/hw_params"
  printf 'Name:\taudio-soundcard\nTgid:\t10\nPid:\t17\n' > "$DXB_PROC/17/status"
  printf '%s\n' '-1 22222222222222222222222222222222 overlapping wall times' '0 11111111111111111111111111111111 overlapping wall times' > "$TEST_TMP/boots"
  : > "$TEST_TMP/events"; : > "$TEST_TMP/capture-calls"
  cat > "$TEST_TMP/journalctl" <<'SH'
#!/bin/bash
printf '%s\n' "$*" >> "$TEST_TMP/capture-calls"
if [[ $1 == --list-boots ]]; then cat "$TEST_TMP/boots"; exit; fi
if [[ -f $TEST_TMP/fail ]]; then echo PRIVATE_ERROR_SENTINEL >&2; exit 1; fi
if [[ -f $TEST_TMP/huge ]]; then head -c 2097153 /dev/zero; exit; fi
cat "$TEST_TMP/events"
SH
  chmod +x "$TEST_TMP/journalctl"
}

test_capture_filters_boots_with_overlapping_and_backwards_wall_time() (
  cap_env
  cat > "$TEST_TMP/events" <<'JSON'
{"_BOOT_ID":"11111111111111111111111111111111","__MONOTONIC_TIMESTAMP":"10","__REALTIME_TIMESTAMP":"500","MESSAGE":"input POLLERR PRIVATE_EVENT_SENTINEL"}
{"_BOOT_ID":"22222222222222222222222222222222","__MONOTONIC_TIMESTAMP":"99","__REALTIME_TIMESTAMP":"500","MESSAGE":"output POLLERR"}
{"_BOOT_ID":"11111111111111111111111111111111","__MONOTONIC_TIMESTAMP":"20","__REALTIME_TIMESTAMP":"100","MESSAGE":"output POLLERR"}
JSON
  local report; report=$(dxb_status_capture)
  assert_eq "$(jq -r '.selected_boot_id' <<< "$report")" 11111111111111111111111111111111
  assert_eq "$(jq '[.journal.input,.journal.output,.journal.rejected_other_boot_records,.journal.monotonic_span_us]' <<< "$report" | jq -c .)" '[1,1,1,10]'
  assert_eq "$(jq -c '.journal.realtime_bounds_us' <<< "$report")" '{"minimum":100,"maximum":500}'
  assert_eq "$(jq -c '[.live.streams[0].owner_task_id,.live.streams[0].owner_process_id,.live.streams[0].sample_rate,.live.streams[0].period_frames,.live.streams[0].buffer_frames]' <<< "$report")" '[17,10,48000,241,482]'
  assert_eq "$(jq '.journal.counts_are_audio_gaps' <<< "$report")" false
  assert_not_contains "$report" PRIVATE_EVENT_SENTINEL
  assert_file_contains "$TEST_TMP/capture-calls" '--boot=11111111111111111111111111111111'
  assert_file_not_contains "$TEST_TMP/capture-calls" --since
)

test_capture_previous_logs_stay_separate_from_live_pcm() (
  cap_env
  printf '%s\n' '{"_BOOT_ID":"22222222222222222222222222222222","__MONOTONIC_TIMESTAMP":"30","__REALTIME_TIMESTAMP":"500","MESSAGE":"input POLLERR"}' > "$TEST_TMP/events"
  local report; report=$(dxb_status_capture previous)
  assert_eq "$(jq -r '.selected_boot_id' <<< "$report")" 22222222222222222222222222222222
  assert_eq "$(jq -r '.live.boot_id' <<< "$report")" 11111111111111111111111111111111
  assert_eq "$(jq '.journal.input' <<< "$report")" 1
  assert_file_contains "$TEST_TMP/capture-calls" '--boot=22222222222222222222222222222222'
)

test_capture_missing_previous_boot_does_not_fall_back_to_current() (
  cap_env
  echo '0 11111111111111111111111111111111 only boot' > "$TEST_TMP/boots"
  local report; report=$(dxb_status_capture previous)
  assert_eq "$(jq -r '.journal.reason' <<< "$report")" boot_unavailable
  assert_eq "$(jq '.selected_boot_id' <<< "$report")" null
  assert_file_not_contains "$TEST_TMP/capture-calls" --unit
)

test_capture_bounds_live_stream_metadata() (
  cap_env
  local i report
  for ((i=1;i<=33;i++)); do
    mkdir -p "$DXB_PROC/asound/card0/pcm0c/sub$i"
    echo closed > "$DXB_PROC/asound/card0/pcm0c/sub$i/status"
  done
  report=$(dxb_status_capture)
  assert_eq "$(jq '.live.streams|length' <<< "$report")" 32
  assert_eq "$(jq '.live.truncated' <<< "$report")" true
)

test_capture_caps_records_and_marks_partial_history() (
  cap_env
  jq -cn 'range(1000;-1;-1)|{_BOOT_ID:"11111111111111111111111111111111",__MONOTONIC_TIMESTAMP:tostring,__REALTIME_TIMESTAMP:tostring,MESSAGE:(if .==1000 then "output POLLERR" else "input POLLERR" end)}' > "$TEST_TMP/events"
  local report; report=$(dxb_status_capture)
  assert_eq "$(jq -c '[.journal.truncated,.journal.records_returned,.journal.records_considered,.journal.input,.journal.output,.journal.monotonic_span_us]' <<< "$report")" '[true,1001,1000,999,1,999]'
  assert_eq "$(jq -c '.journal.realtime_bounds_us' <<< "$report")" '{"minimum":1,"maximum":1000}'
  assert_file_contains "$TEST_TMP/capture-calls" '--reverse'
)

test_capture_unknown_timestamps_do_not_invent_an_interval() (
  cap_env
  printf '%s\n' '{"_BOOT_ID":"11111111111111111111111111111111","__REALTIME_TIMESTAMP":"500","MESSAGE":"input POLLERR"}' > "$TEST_TMP/events"
  local report; report=$(dxb_status_capture)
  assert_eq "$(jq -c '[.journal.input,.journal.monotonic_timestamps_complete,.journal.monotonic_span_us]' <<< "$report")" '[1,false,null]'
)

test_capture_missing_boot_identity_does_not_guess_from_wall_time() (
  cap_env
  rm "$DXB_PROC/sys/kernel/random/boot_id"
  local report; report=$(dxb_status_capture)
  assert_eq "$(jq -r '.journal.reason' <<< "$report")" boot_unavailable
  assert_eq "$(cat "$TEST_TMP/capture-calls")" ''
)

test_capture_journal_failures_are_unavailable_and_redacted() (
  cap_env
  touch "$TEST_TMP/fail"
  local report; report=$(dxb_status_capture)
  assert_eq "$(jq -r '.journal.reason' <<< "$report")" journal_unavailable
  assert_not_contains "$report" PRIVATE_ERROR_SENTINEL
  rm "$TEST_TMP/fail"; echo 'not JSON' > "$TEST_TMP/events"
  report=$(dxb_status_capture)
  assert_eq "$(jq -r '.journal.reason' <<< "$report")" invalid_journal
  echo '{}' > "$TEST_TMP/events"
  report=$(dxb_status_capture)
  assert_eq "$(jq -r '.journal.reason' <<< "$report")" invalid_journal
  touch "$TEST_TMP/huge"
  report=$(dxb_status_capture)
  assert_eq "$(jq -r '.journal.reason' <<< "$report")" output_limit
  cat > "$TEST_TMP/timeout" <<'SH'
#!/bin/bash
exit 124
SH
  chmod +x "$TEST_TMP/timeout"; export DXB_CAPTURE_TIMEOUT=$TEST_TMP/timeout
  report=$(dxb_status_capture)
  assert_eq "$(jq -r '.journal.reason' <<< "$report")" deadline
)

test_capture_closed_pcm_keeps_unknown_values_null() (
  cap_env
  echo closed > "$DXB_PROC/asound/card0/pcm0c/sub0/status"
  echo closed > "$DXB_PROC/asound/card0/pcm0c/sub0/hw_params"
  local report; report=$(dxb_status_capture)
  assert_eq "$(jq -c '[.live.streams[0].state,.live.streams[0].owner_task_id,.live.streams[0].owner_process_id,.live.streams[0].format,.live.streams[0].sample_rate]' <<< "$report")" '["closed",null,null,null,null]'
)

test_capture_cli_is_explicit_and_not_a_periodic_report_part() (
  cap_env
  export DXB_FAKE_EUID=0
  local report; report=$("$DXB_ROOT/provision/bin/dxberry-status" --json --capture)
  assert_eq "$(jq -r .kind <<< "$report")" capture-diagnostics
  assert_fails "$DXB_ROOT/provision/bin/dxberry-status" --capture
  assert_fails "$DXB_ROOT/provision/bin/dxberry-status" --json --capture pi
  assert_fails "$DXB_ROOT/provision/bin/dxberry-status" --json --previous-boot
  export DXB_FAKE_EUID=1000
  assert_fails "$DXB_ROOT/provision/bin/dxberry-status" --json --capture
)
