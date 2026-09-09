#!/usr/bin/env bash
# shellcheck disable=SC1091,SC2034,SC2317
source "$DXB_LIB/common.sh"
source "$DXB_LIB/config.sh"
source "$DXB_LIB/graywolf.sh"
source "$DXB_LIB/radio.sh"
source "$DXB_LIB/apps/graywolf.sh"

gwapp_env() {
  export DXB_STATE_DIR=$TEST_TMP/state DXB_LOG_FILE=$TEST_TMP/state/log DXB_GW_COOKIES=$TEST_TMP/cookies \
    DXB_GW_API=http://gw/api DXB_GW_SECRET_FILE=$TEST_TMP/state/graywolf.secret DXB_RADIOS_FILE=$TEST_TMP/state/radios.json
  mkdir -p "$DXB_STATE_DIR"; : > "$TEST_TMP/calls"
  printf 'USER=admin\nPASSWORD=hunter2hunter2\n' > "$DXB_GW_SECRET_FILE"
  DXB_CURL=gwapp_curl; sleep() { :; }
  GW_AUDIO='[]'; GW_CHANNELS='[]'; GW_PTT_404=1; GW_FAIL_PATH=''; GW_FAIL_ON_CALL=''
  declare -gA GW_CALL_COUNT=()
  DXB_RADIOS='{"version":1,"radios":{"radio1":{"label":"TM-V71","audio":{"path":"usb-0:1.3:1.0"},"cat":{"path":"usb-0:1.4:1.0"},"hid":{"path":"usb-0:1.3:1.3"},"ptt_serial":null,"ptt":{"method":"rigctld","gpio_line":null},"rig":{"model":1,"baud":57600,"ptt_type":"RTS"},"rigctld_port":4532,"wiring":"full","owner":""}},"gps":{}}'
}
# Records "METHOD PATH BODY"; answers from GW_AUDIO / GW_CHANNELS; POST returns {"id":N}. GW_FAIL_PATH forces one path
# to fail; with GW_FAIL_ON_CALL unset every call to that path fails, otherwise only the GW_FAIL_ON_CALL'th call to it does.
gwapp_curl() {
  local url='' m=GET data='' via=0
  while (( $# )); do case $1 in -X) m=$2; shift ;; --data-binary) data=$2; shift ;; -b|-c) via=1 ;; http*) url=$1 ;; esac; shift; done
  [[ $data == @-* ]] && data=$(cat)
  local p=${url#"$DXB_GW_API"}
  echo "$m $p $data" >> "$TEST_TMP/calls"
  if [[ -n $GW_FAIL_PATH && $p == "$GW_FAIL_PATH" ]]; then
    GW_CALL_COUNT[$p]=$(( ${GW_CALL_COUNT[$p]:-0} + 1 ))
    if [[ -z $GW_FAIL_ON_CALL ]] || (( GW_CALL_COUNT[$p] == GW_FAIL_ON_CALL )); then
      return 22
    fi
  fi
  case "$m $p" in
    "GET /auth/setup") echo '{"needs_setup":false}' ;;
    "POST /auth/login"|"POST /auth/logout") echo '{}' ;;
    "GET /audio-devices") echo "$GW_AUDIO" ;;
    "POST /audio-devices") echo '{"id":11}' ;;
    "PUT /audio-devices/"*) echo '{"id":11}' ;;
    "DELETE /audio-devices/"*) echo '{}' ;;
    "GET /channels") echo "$GW_CHANNELS" ;;
    "POST /channels") echo '{"id":21}' ;;
    "PUT /channels/"*) echo '{"id":21}' ;;
    "GET /channels/"*) echo '{"id":21,"name":"radio1"}' ;;
    "DELETE /channels/"*) echo '{}' ;;
    "GET /ptt/"*) (( GW_PTT_404 )) && return 22; echo '{"id":31,"channel_id":21,"method":"vox","dwait_ms":30}' ;;
    "POST /ptt"|"PUT /ptt/"*) echo '{"id":31}' ;;
    "POST /ptt/test-rigctld") echo '{"ok":true,"message":"","latency_ms":3}' ;;
    *) return 22 ;;
  esac
}
# named for this file: every tests/test_*.sh is sourced into one process, so a bare calls()
# here would be silently replaced by another file's helper of the same name
gwapp_calls() { cat "$TEST_TMP/calls"; }

test_gwapp_wire_creates_device_channel_and_rigctld_ptt() {
  gwapp_env
  assert_ok app_graywolf_wire radio1
  assert_contains "$(gwapp_calls)" 'POST /auth/login {"username":"admin","password":"hunter2hunter2"}'
  assert_contains "$(gwapp_calls)" 'POST /audio-devices {"name":"radio1","source_type":"soundcard","source_path":"plughw:CARD=RADIO1,DEV=0","sample_rate":48000}'
  assert_contains "$(gwapp_calls)" 'POST /channels {"name":"radio1","input_device_id":11,"output_device_id":11,"input_channel":0,"output_channel":0}'
  assert_contains "$(gwapp_calls)" 'POST /ptt {"channel_id":21,"method":"rigctld","device_path":"127.0.0.1:4532","invert":false,"persist":true}'
  assert_contains "$(gwapp_calls)" 'POST /ptt/test-rigctld {"host":"127.0.0.1","port":4532}'
  assert_contains "$(gwapp_calls)" 'POST /auth/logout'
}

test_gwapp_wire_updates_existing_by_name_and_keeps_tuning() {
  gwapp_env
  GW_AUDIO='[{"id":5,"name":"other"},{"id":11,"name":"radio1","source_path":"plughw:0,0","gain_db":-6}]'
  GW_CHANNELS='[{"id":21,"name":"radio1","input_device_id":5,"output_device_id":5,"modem_type":"afsk1200","num_slicers":5}]'
  GW_PTT_404=0
  assert_ok app_graywolf_wire radio1
  assert_contains "$(gwapp_calls)" 'PUT /audio-devices/11 {"name":"radio1","source_path":"plughw:CARD=RADIO1,DEV=0","gain_db":-6,"source_type":"soundcard","sample_rate":48000}'
  assert_contains "$(gwapp_calls)" 'PUT /channels/21 {"name":"radio1","input_device_id":11,"output_device_id":11,"modem_type":"afsk1200","num_slicers":5,"input_channel":0,"output_channel":0}'
  assert_contains "$(gwapp_calls)" 'PUT /ptt/21 {"channel_id":21,"method":"rigctld","dwait_ms":30,"device_path":"127.0.0.1:4532","invert":false,"persist":true}'
  assert_not_contains "$(gwapp_calls)" 'POST /audio-devices'
}

test_gwapp_ptt_payloads_per_method() {
  gwapp_env
  local r; r=$(jq -c '.radios.radio1' <<< "$DXB_RADIOS")
  assert_eq "$(dxb_gwapp_ptt_payload radio1 "$(jq -c '.ptt.method="cm108"' <<< "$r")" 21)" '{"channel_id":21,"method":"cm108","device_path":"/dev/dxberry/radio1-hid","gpio_pin":3,"invert":false,"persist":true}'
  assert_eq "$(dxb_gwapp_ptt_payload radio1 "$(jq -c '.ptt.method="gpio" | .ptt.gpio_line=17' <<< "$r")" 21)" '{"channel_id":21,"method":"gpio","device_path":"/dev/gpiochip0","gpio_line":17,"invert":false,"persist":true}'
  assert_eq "$(dxb_gwapp_ptt_payload radio1 "$(jq -c '.ptt.method="vox"' <<< "$r")" 21)" '{"channel_id":21,"method":"vox","invert":false,"persist":true}'
}

test_gwapp_wire_fails_with_7_on_api_error() {
  gwapp_env; GW_FAIL_PATH=/channels
  app_graywolf_wire radio1; assert_eq "$?" "7"
  gwapp_env; GW_FAIL_PATH=/ptt/test-rigctld
  assert_ok app_graywolf_wire radio1                                   # connectivity test failure is a warning only
}

test_gwapp_wire_fails_on_failed_list_fetch_without_duplicating() {
  gwapp_env
  GW_FAIL_PATH=/audio-devices; GW_FAIL_ON_CALL=1
  app_graywolf_wire radio1
  assert_eq "$?" "7"
  assert_not_contains "$(gwapp_calls)" "POST /audio-devices"
}

test_gwapp_wire_put_bodies_are_never_empty() {
  gwapp_env
  GW_AUDIO='[{"id":11,"name":"radio1","source_path":"plughw:0,0"}]'
  GW_CHANNELS='[{"id":21,"name":"radio1","input_device_id":11,"output_device_id":11}]'
  GW_PTT_404=0
  assert_ok app_graywolf_wire radio1
  local line m p body puts=0
  while IFS= read -r line; do
    [[ $line == PUT\ * ]] || continue
    puts=$(( puts + 1 ))
    read -r m p body <<< "$line"
    assert_eq "${body:0:1}" "{"
  done <<< "$(gwapp_calls)"
  # the audio device and the channel are both updates here: a loop that inspected nothing would
  # otherwise pass silently (it did, while a helper named calls() from another test file won)
  (( puts >= 2 )) || _fail "expected at least 2 PUT calls to inspect, saw $puts"
}

test_gwapp_unwire_deletes_by_name_and_tolerates_absence() {
  gwapp_env
  GW_AUDIO='[{"id":11,"name":"radio1"}]'; GW_CHANNELS='[{"id":21,"name":"radio1"}]'
  assert_ok app_graywolf_unwire radio1
  assert_contains "$(gwapp_calls)" 'DELETE /channels/21?cascade=true'
  assert_contains "$(gwapp_calls)" 'DELETE /audio-devices/11'
  gwapp_env
  assert_ok app_graywolf_unwire radio1
  assert_not_contains "$(gwapp_calls)" 'DELETE'
}

test_gwapp_contract_functions() {
  gwapp_env
  assert_eq "$(app_graywolf_unit)" "graywolf.service"
  assert_eq "$(app_graywolf_needs_service_restart)" "no"
  assert_ok app_graywolf_wait_ready
}
