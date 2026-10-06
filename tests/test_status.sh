#!/usr/bin/env bash
# shellcheck disable=SC1091,SC2034,SC2317
source "$DXB_LIB/common.sh"
source "$DXB_LIB/graywolf.sh"
source "$DXB_LIB/status.sh"

# st_env: a Pi 4 on WiFi, chrony synced over NTP, Graywolf running with one channel, no radios.
# The fake commands answer from files under $TEST_TMP, so a test changes the scene by editing one.
st_env() {
  export DXB_STATE_DIR=$TEST_TMP/state DXB_LOG_FILE=$TEST_TMP/state/log DXB_PROC=$TEST_TMP/proc \
    DXB_SYSFS_ROOT=$TEST_TMP/sys DXB_DT_MODEL=$TEST_TMP/proc/device-tree/model DXB_DF_PATH=/ \
    DXB_VCGENCMD=st_vcgencmd DXB_CHRONYC=st_chronyc DXB_IW=st_iw DXB_NETWATCH=st_netwatch DXB_RADIO_CMD=st_radio \
    DXB_RELEASE_FILE=$TEST_TMP/dxberry-release DXB_RUN_DIR=$TEST_TMP/run DXB_RIGCTLD_RUN_DIR=$TEST_TMP/run/rigctld \
    DXB_STATUS_GW_COOKIES=$TEST_TMP/run/status-graywolf.cookies DXB_STATUS_CURL=st_curl DXB_GW_API=http://gw/api \
    DXB_GW_SECRET_FILE=$TEST_TMP/state/graywolf.secret
  mkdir -p "$DXB_STATE_DIR" "$DXB_PROC/device-tree" "$DXB_SYSFS_ROOT/class/thermal/thermal_zone0" "$DXB_RIGCTLD_RUN_DIR" "$TEST_TMP/units"
  printf 'Raspberry Pi 4 Model B Rev 1.4\0' > "$DXB_DT_MODEL"
  echo 36998 > "$DXB_SYSFS_ROOT/class/thermal/thermal_zone0/temp"
  echo '0.12 0.10 0.09 1/180 4242' > "$DXB_PROC/loadavg"
  printf 'MemTotal:        3884332 kB\nMemFree:          100000 kB\nMemAvailable:    3400000 kB\n' > "$DXB_PROC/meminfo"
  echo '450123.45 1700000.12' > "$DXB_PROC/uptime"
  echo 0x50000 > "$TEST_TMP/throttled"
  echo 'WIFI (wlan0 holds the address)' > "$TEST_TMP/netwatch"
  printf 'DXBERRY_VERSION=0.3.0-rc1\nDXBERRY_COMMIT=abc1234\n' > "$DXB_RELEASE_FILE"
  ( umask 077; printf 'USER=admin\nPASSWORD=gwsecret1\n' > "$DXB_GW_SECRET_FILE" )
  printf 'LoadState=loaded\nActiveState=active\nSubState=running\nResult=success\nType=simple\nUnitFileState=enabled\n' > "$TEST_TMP/units/graywolf.service"
  printf 'LoadState=loaded\nActiveState=active\nSubState=listening\nResult=success\nUnitFileState=enabled\n' > "$TEST_TMP/units/cockpit.socket"
  printf 'LoadState=loaded\nActiveState=inactive\nSubState=dead\nResult=success\nType=oneshot\nUnitFileState=enabled\n' > "$TEST_TMP/units/dxberry-radio-hotplug.service"
  : > "$TEST_TMP/calls"; : > "$TEST_TMP/argv"
}

st_vcgencmd() { [[ -f $TEST_TMP/throttled ]] || return 1; echo "throttled=$(< "$TEST_TMP/throttled")"; }
st_netwatch() { cat "$TEST_TMP/netwatch"; }
st_iw() { printf 'Connected to 52:eb:f6:35:28:a1 (on wlan0)\n\tSSID: Shack Net\n\tfreq: 2432.0\n\tsignal: -47 dBm\n'; }
st_chronyc() {
  [[ -f $TEST_TMP/chrony-down ]] && return 1
  case $2 in
    tracking)
      if [[ -f $TEST_TMP/chrony-unsynced ]]; then
        echo '00000000,,0,0.000000000,0.000000000,0.000000000,0.000000000,0.000,0.000,0.000,1.000000000,1.000000000,0.0,Not synchronised'
      else
        echo '90CA42D6,144.202.66.214,5,1791286985.948875017,-0.000412000,0.000676516,0.000807318,9.683,0.015,0.207,0.051771447,0.003547146,1038.3,Normal'
      fi ;;
    sources)
      if [[ -f $TEST_TMP/chrony-unsynced ]]; then
        printf '^,?,144.202.66.214,4,10,0,652,-0.001074566,-0.000398051,0.028204454\n'
      elif [[ -f $TEST_TMP/chrony-gps ]]; then
        printf '#,*,GPS,0,4,377,8,0.000012000,0.000012000,0.000100000\n^,+,144.202.66.214,4,10,377,652,-0.001074566,-0.000398051,0.028204454\n'
      else
        printf '^,*,144.202.66.214,4,10,377,652,-0.001074566,-0.000398051,0.028204454\n^,+,23.186.168.133,2,10,377,1716,-0.002263054,-0.001647987,0.057753824\n'
      fi ;;
  esac
}
st_radio() {
  [[ -f $TEST_TMP/radio-fails ]] && return 6
  if [[ -f $TEST_TMP/radio.json ]]; then cat "$TEST_TMP/radio.json"; else echo '{"radios":{},"gps":{"fix":0,"receiver":false}}'; fi
}
# st_curl: Graywolf's API as dxb_gw_api calls it. A login with the stored password opens a session
# (a file); every other call needs that session. argv goes to $TEST_TMP/argv so a test can prove
# the password never travels on a command line. Mimics curl -f: an HTTP error is exit 22 with
# "curl: (22) The requested URL returned error: <code>" on stderr. A test can make one path always
# answer 404 (regardless of session) by touching $TEST_TMP/gw-404-<name>, where <name> is the
# path's first segment (igate, channels, ...).
st_curl() {
  local m=GET url='' data='' p name
  printf '%s\n' "$*" >> "$TEST_TMP/argv"
  while (( $# )); do
    case $1 in
      -X) m=$2; shift ;;
      --data-binary) data=$2; shift ;;
      http*) url=$1 ;;
    esac
    shift
  done
  [[ $data == @- ]] && data=$(cat)
  p=${url#"$DXB_GW_API"}
  echo "$m $p" >> "$TEST_TMP/calls"
  [[ -f $TEST_TMP/gw-down ]] && return 7
  if [[ $m == POST && $p == /auth/login ]]; then
    if [[ $(jq -r .password <<< "$data") == gwsecret1 ]]; then
      : > "$TEST_TMP/gw-session"; echo '{"ok":true}'; return 0
    fi
    echo 'curl: (22) The requested URL returned error: 401' >&2
    return 22
  fi
  name=${p#/}; name=${name%%/*}
  if [[ -f $TEST_TMP/gw-404-$name ]]; then
    echo 'curl: (22) The requested URL returned error: 404' >&2
    return 22
  fi
  if [[ ! -f $TEST_TMP/gw-session ]]; then
    echo 'curl: (22) The requested URL returned error: 401' >&2
    return 22
  fi
  case $p in
    /channels) echo '[{"id":3,"name":"VHF APRS","enabled":true,"mode":"aprs"}]' ;;
    /channels/3/stats) echo '{"channel":3,"rx_frames":6228,"rx_bad_fcs":5044,"tx_frames":1010,"dcd_state":false}' ;;
    /igate) echo '{"connected":true,"server":"rotate.aprs2.net:14580","callsign":"N0CALL-10","rf_to_is_gated":2719,"is_to_rf_gated":1}' ;;
    /position-log) jq -cn --arg p "$TEST_TMP/run/graywolf/history.db" '{enabled: true, db_path: $p}' ;;
    *) echo '{}' ;;
  esac
}
# st_stubs: the external commands status.sh calls by their real names. Defined only inside a test's
# subshell (st_run, st_cli) so they never leak into the other test files sharing this shell.
st_stubs() {
  systemctl() {
    echo "systemctl $*" >> "$TEST_TMP/calls"
    [[ $1 == show ]] || return 0
    local f=$TEST_TMP/units/${*: -1}
    if [[ -f $f ]]; then cat "$f"; else printf 'LoadState=not-found\nActiveState=inactive\nSubState=dead\nResult=success\nType=\nUnitFileState=\n'; fi
  }
  ip() {
    case "$*" in
      *'addr show dev wlan0'*) echo '3: wlan0    inet 10.0.0.90/24 brd 10.0.0.255 scope global wlan0\       valid_lft forever preferred_lft forever' ;;
      *'addr show dev eth0'*) echo '2: eth0    inet 10.0.0.90/24 brd 10.0.0.255 scope global eth0\       valid_lft forever preferred_lft forever' ;;
      *'route show default'*) echo 'default via 10.0.0.1 dev wlan0 onlink' ;;
    esac
  }
  df() { printf 'Filesystem 1-blocks Used Available Capacity Mounted on\n/dev/sda2 30000000000 3000000000 27000000000 10%% /\n'; }
  dpkg-query() { case ${*: -1} in graywolf) echo 'installed 0.14.13' ;; cockpit-ws) echo 'installed 337-1+deb13u2' ;; *) return 1 ;; esac; }
  hostname() { echo dxberry-pi; }
}
st_run() { ( st_stubs; "$@" ); }

test_status_pi_reads_model_temperature_throttling_load_memory_disk_and_uptime() {
  st_env
  local j; j=$(st_run dxb_status_pi)
  assert_eq "$(jq -r .model <<< "$j")" "Raspberry Pi 4 Model B Rev 1.4"
  assert_eq "$(jq -c '[.temp_c == 37, .load == [0.12, 0.1, 0.09], .mem_total, .mem_used, .disk_total, .disk_used, .uptime_s]' <<< "$j")" \
    '[true,true,3977555968,495955968,30000000000,3000000000,450123]'
  assert_eq "$(jq -c '.throttle | [.raw, .under_voltage_now, .throttled_now, .under_voltage_since_boot, .throttled_since_boot, .freq_capped_since_boot]' <<< "$j")" \
    '["0x50000",false,false,true,true,false]'
}

test_status_pi_without_vcgencmd_or_a_sensor_still_reports_the_rest() {
  st_env
  rm -f "$TEST_TMP/throttled" "$DXB_SYSFS_ROOT/class/thermal/thermal_zone0/temp"
  local j; j=$(st_run dxb_status_pi)
  assert_eq "$(jq -c '[.throttle, .temp_c, .model]' <<< "$j")" '[null,null,"Raspberry Pi 4 Model B Rev 1.4"]'
}

test_status_network_on_wifi_reports_address_gateway_ssid_and_signal() {
  st_env
  local j; j=$(st_run dxb_status_network)
  assert_eq "$(jq -c '[.state, .interface, .address, .prefix, .gateway, .wifi.ssid, .wifi.signal_dbm, .hostname]' <<< "$j")" \
    '["WIFI","wlan0","10.0.0.90",24,"10.0.0.1","Shack Net",-47,"dxberry-pi"]'
}

test_status_network_on_ethernet_or_none_has_no_wifi_block() {
  st_env
  echo 'ETH (eth0 holds the address)' > "$TEST_TMP/netwatch"
  assert_eq "$(st_run dxb_status_network | jq -c '[.state, .interface, .address, .wifi]')" '["ETH","eth0","10.0.0.90",null]'
  echo 'NONE' > "$TEST_TMP/netwatch"
  assert_eq "$(st_run dxb_status_network | jq -c '[.state, .interface, .address, .prefix, .wifi]')" '["NONE","","",null,null]'
}

test_status_time_reports_sync_source_and_offset() {
  st_env
  assert_eq "$(st_run dxb_status_time | jq -c '[.synced, .source, .reference, .stratum, .offset_ms]')" '[true,"NTP","144.202.66.214",5,-0.412]'
  : > "$TEST_TMP/chrony-gps"
  assert_eq "$(st_run dxb_status_time | jq -r .source)" "GPS"
  rm -f "$TEST_TMP/chrony-gps"; : > "$TEST_TMP/chrony-unsynced"
  assert_eq "$(st_run dxb_status_time | jq -c '[.synced, .source]')" '[false,"none"]'
}

test_status_release_reads_the_installed_versions() {
  st_env
  assert_eq "$(st_run dxb_status_release | jq -c '[.dxberry, .dxberry_commit, .graywolf, .cockpit, .update]')" \
    '["0.3.0-rc1","abc1234","0.14.13","337-1+deb13u2",null]'
}

test_status_services_lists_the_station_units_and_each_rigctld() {
  st_env
  : > "$DXB_RIGCTLD_RUN_DIR/radio1.env"
  local j; j=$(st_run dxb_status_services)
  assert_eq "$(jq -r '[.units[].unit] | join(" ")' <<< "$j")" \
    "graywolf.service rigctld@radio1.service gpsd.service chrony.service dxberry-netwatch.service dxberry-radio-hotplug.service dxberry-radio-wire.service cockpit.socket"
  assert_eq "$(jq -c '.units[0] | [.load, .active, .sub, .result, .type, .enabled]' <<< "$j")" '["loaded","active","running","success","simple","enabled"]'
  assert_eq "$(jq -c '.units[] | select(.unit == "dxberry-radio-wire.service") | [.load, .active]' <<< "$j")" '["not-found","inactive"]'
  assert_eq "$(jq -c '.units[] | select(.unit == "dxberry-radio-hotplug.service") | [.type, .result]' <<< "$j")" '["oneshot","success"]'
}

test_status_json_keeps_the_other_parts_when_one_fails() {
  st_env
  : > "$TEST_TMP/chrony-down"
  local j; j=$(st_run dxb_status_json pi time release)
  assert_eq "$(jq -c '[.pi.ok, .time.ok, .time.error, .release.ok]' <<< "$j")" '[true,false,"chrony is not answering",true]'
  assert_eq "$(jq -r 'keys | join(" ")' <<< "$j")" "generated pi release time"
  [[ $(jq -r .generated <<< "$j") =~ ^20[0-9]{2}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$ ]] || _fail "generated is not a UTC timestamp"
}

test_status_graywolf_reports_channels_igate_and_position_log_size() {
  st_env
  mkdir -p "$TEST_TMP/run/graywolf"
  head -c 1000 /dev/zero > "$TEST_TMP/run/graywolf/history.db"
  head -c 24 /dev/zero > "$TEST_TMP/run/graywolf/history.db-wal"
  local j; j=$(st_run dxb_status_graywolf)
  assert_eq "$(jq -c '[.active, .sub, .version, .web_port, .api_ok, .api_error]' <<< "$j")" '["active","running","0.14.13",8080,true,""]'
  assert_eq "$(jq -c '.channels' <<< "$j")" '[{"id":3,"name":"VHF APRS","enabled":true,"rx_frames":6228,"tx_frames":1010,"rx_bad_fcs":5044}]'
  assert_eq "$(jq -c '.igate' <<< "$j")" '{"connected":true,"server":"rotate.aprs2.net:14580","rf_to_is_gated":2719,"is_to_rf_gated":1}'
  assert_eq "$(jq -c '[.position_log.enabled, .position_log.bytes]' <<< "$j")" '[true,1024]'
}

test_status_graywolf_logs_in_once_and_keeps_the_session() {
  st_env
  st_run dxb_status_graywolf > /dev/null
  st_run dxb_status_graywolf > /dev/null
  assert_eq "$(grep -c '^POST /auth/login$' "$TEST_TMP/calls")" "1"
  assert_file_not_contains "$TEST_TMP/argv" "gwsecret1"
  assert_eq "$(stat -c %a "$DXB_STATUS_GW_COOKIES")" "600"
  # an expired session costs one more login, not a failure
  rm -f "$TEST_TMP/gw-session"
  assert_eq "$(st_run dxb_status_graywolf | jq -r .api_ok)" "true"
  assert_eq "$(grep -c '^POST /auth/login$' "$TEST_TMP/calls")" "2"
}

test_status_graywolf_stopped_reports_its_state_without_calling_the_api() {
  st_env
  printf 'LoadState=loaded\nActiveState=failed\nSubState=failed\nResult=exit-code\n' > "$TEST_TMP/units/graywolf.service"
  assert_eq "$(st_run dxb_status_graywolf | jq -c '[.active, .result, .api_ok, .api_error, .channels, .igate, .position_log]')" \
    '["failed","exit-code",false,"graywolf is not running",[],null,null]'
  assert_eq "$(cat "$TEST_TMP/argv")" ""
}

test_status_graywolf_explains_a_missing_refused_or_silent_login() {
  st_env
  rm -f "$DXB_GW_SECRET_FILE"
  assert_contains "$(st_run dxb_status_graywolf | jq -r .api_error)" "no stored Graywolf login"
  printf 'USER=admin\nPASSWORD=wrongpass1\n' > "$DXB_GW_SECRET_FILE"
  assert_eq "$(st_run dxb_status_graywolf | jq -r .api_error)" "Graywolf refused the stored login"
  printf 'USER=admin\nPASSWORD=gwsecret1\n' > "$DXB_GW_SECRET_FILE"
  : > "$TEST_TMP/gw-down"
  assert_contains "$(st_run dxb_status_graywolf | jq -r .api_error)" "did not answer"
}

# An endpoint that keeps failing for a reason other than authentication (a 404 on another
# Graywolf version, a disabled channel's stats, ...) must never cost a fresh login: that is a
# database write on Graywolf's side, and this runs every 10 seconds.
test_status_graywolf_a_404_never_forces_a_relogin() {
  st_env
  : > "$TEST_TMP/gw-404-igate"
  local j
  for _ in 1 2 3; do j=$(st_run dxb_status_graywolf); done
  assert_eq "$(grep -c '^POST /auth/login$' "$TEST_TMP/calls")" "1"
  assert_eq "$(jq -c '[.api_ok, .igate]' <<< "$j")" '[true,null]'
}

test_status_radios_passes_dxberry_radio_status_through() {
  st_env
  echo '{"radios":{"radio1":{"label":"TM-V71","present":true,"owner":"graywolf","rigctld":"active","rigctld_port":4532,"freq":"145390000","mode":"FM"}},"gps":{"fix":0,"receiver":false}}' > "$TEST_TMP/radio.json"
  assert_eq "$(st_run dxb_status_radios | jq -c '[.radios.radio1.owner, .radios.radio1.freq, .gps.receiver]')" '["graywolf","145390000",false]'
  : > "$TEST_TMP/radio-fails"
  assert_eq "$(st_run dxb_status_json radios | jq -c '[.radios.ok, .radios.error]')" '[false,"dxberry-radio status failed (exit 6)"]'
}

# st_cli ARGS...: the real command in a subshell with the stubs; stdout to $TEST_TMP/out.
st_cli() {
  (
    source "$DXB_ROOT/provision/bin/dxberry-status"
    dxb_require_root() { :; }
    st_stubs
    main "$@"
  ) > "$TEST_TMP/out" 2> "$TEST_TMP/err"
}
st_out() { cat "$TEST_TMP/out"; }

test_status_cli_json_has_every_part() {
  st_env
  assert_ok st_cli --json
  assert_eq "$(jq -r 'keys | join(" ")' "$TEST_TMP/out")" "generated graywolf network pi radios release services time"
  assert_eq "$(jq -r '[.[] | objects | .ok] | all' "$TEST_TMP/out")" "true"
}

test_status_cli_prints_a_readable_report() {
  st_env
  assert_ok st_cli
  assert_contains "$(st_out)" "pi: Raspberry Pi 4 Model B Rev 1.4, 99°F (37°C)"
  assert_contains "$(st_out)" "under-voltage since boot"
  assert_contains "$(st_out)" 'network: WIFI on wlan0 10.0.0.90/24 via 10.0.0.1, WiFi "Shack Net" -47 dBm'
  assert_contains "$(st_out)" "graywolf: running, version 0.14.13, iGate connected to rotate.aprs2.net:14580, channel VHF APRS rx 6228 tx 1010 bad FCS 5044"
  assert_contains "$(st_out)" "time: synced to NTP 144.202.66.214, offset -0.412 ms"
  assert_contains "$(st_out)" "release: DXBerry 0.3.0-rc1, Graywolf 0.14.13, Cockpit 337-1+deb13u2"
  assert_contains "$(st_out)" "  dxberry-radio-wire.service: not installed"
}

test_status_cli_takes_part_names_and_rejects_anything_else() {
  st_env
  assert_ok st_cli --json pi time
  assert_eq "$(jq -r 'keys | join(" ")' "$TEST_TMP/out")" "generated pi time"
  st_cli bogus; assert_eq "$?" "2"
  assert_contains "$(cat "$TEST_TMP/err")" "unknown part: bogus"
  st_cli --frobnicate; assert_eq "$?" "2"
  assert_ok st_cli -h
  assert_contains "$(st_out)" "dxberry-status [--json] [PART...]"
}

# The console runs this every 10 seconds: nothing it does may grow provision.log, even when a
# collector along the way calls a dxb_ logging helper (dxb_warn, dxb_info, ...). st_vcgencmd is
# overridden only inside this subshell, so the simulated warning never reaches the other tests.
# A marker file (outside dxb_log's own redirections) proves the override actually ran, so this
# test cannot pass vacuously.
test_status_cli_leaves_the_provision_log_alone() {
  st_env
  rm -f "$DXB_GW_SECRET_FILE"
  (
    source "$DXB_ROOT/provision/bin/dxberry-status"
    dxb_require_root() { :; }
    st_stubs
    st_vcgencmd() { echo called >> "$TEST_TMP/vcgencmd-called"; dxb_warn "simulated: vcgencmd failed"; return 1; }
    main --json
  ) > "$TEST_TMP/out" 2> "$TEST_TMP/err"
  [[ -f $TEST_TMP/vcgencmd-called ]] || _fail "the simulated vcgencmd override never ran"
  # shellcheck disable=SC2031  # dxberry-status overrides DXB_LOG_FILE only inside the subshell above; this checks the outer value st_env set
  [[ -s $DXB_LOG_FILE ]] && _fail "dxberry-status wrote to $DXB_LOG_FILE"
  return 0
}

# A part can be ok:true (or api_ok:true) and still carry a null where the renderer expects an
# array or object (an empty-but-present field from some future collector change, a hand-built
# report, etc). One such null must never take down the other parts' lines.
test_status_text_tolerates_null_arrays_in_otherwise_ok_parts() {
  local j out rc
  j=$(jq -cn '{
    generated: "2026-01-01T00:00:00Z",
    pi: {ok: true, model: "Raspberry Pi 4 Model B Rev 1.4", temp_c: 37, throttle: null, load: null,
         mem_total: 3977555968, mem_used: 495955968, disk_total: 30000000000, disk_used: 3000000000, uptime_s: 450123},
    network: {ok: true, state: "WIFI", interface: "wlan0", address: "10.0.0.90", prefix: 24, gateway: "10.0.0.1", wifi: null, hostname: "dxberry-pi"},
    graywolf: {ok: true, active: "active", sub: "running", result: "success", version: "0.14.13", web_port: 8080,
               api_ok: true, api_error: "", igate: null, channels: null, position_log: null},
    radios: {ok: true, radios: null, gps: {fix: 0, receiver: false}},
    time: {ok: true, synced: true, source: "NTP", reference: "144.202.66.214", stratum: 5, offset_ms: -0.412},
    release: {ok: true, dxberry: "0.3.0-rc1", dxberry_commit: "abc1234", graywolf: "0.14.13", cockpit: "337-1+deb13u2", update: null},
    services: {ok: true, units: null}
  }')
  out=$(dxb_status_text <<< "$j"); rc=$?
  assert_eq "$rc" "0"
  assert_contains "$out" "pi: Raspberry Pi 4 Model B Rev 1.4"
  assert_contains "$out" "network: WIFI on wlan0 10.0.0.90/24 via 10.0.0.1"
  assert_contains "$out" "graywolf: running, version 0.14.13"
  assert_contains "$out" "radios: 0 radio(s)"
  assert_contains "$out" "time: synced to NTP 144.202.66.214, offset -0.412 ms"
  assert_contains "$out" "release: DXBerry 0.3.0-rc1, Graywolf 0.14.13, Cockpit 337-1+deb13u2"
  assert_contains "$out" "services:"
}
