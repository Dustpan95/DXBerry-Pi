#!/usr/bin/env bash
# shellcheck disable=SC1091,SC2034,SC2317
source "$DXB_LIB/common.sh"
source "$DXB_LIB/config.sh"
source "$DXB_LIB/graywolf.sh"

gw_env() {
  export DXB_STATE_DIR=$TEST_TMP/state DXB_LOG_FILE=$TEST_TMP/state/log DXB_GW_COOKIES=$TEST_TMP/cookies \
    DXB_GW_SEED_STATE=$TEST_TMP/state/graywolf-seed.env DXB_GW_API=http://gw/api DXB_GW_RELEASES=http://rel DXB_DPKG_ARCH=arm64 DXB_ZONEINFO_DIR=$TEST_TMP/nozone \
    DXB_GW_SECRET_FILE=$TEST_TMP/state/graywolf.secret
  mkdir -p "$DXB_STATE_DIR" "$TEST_TMP/http"
  : > "$TEST_TMP/calls"
  DXB_STATUS_LINES=(); DXB_FAILED_STEPS=(); DXB_CONSUMED_SECRETS=''
  DXB_CURL=fake_curl
  apt-get() { echo "apt-get $*" >> "$TEST_TMP/calls"; }
  # Reports libasound.so.2 present by default, so tests that don't care about the ALSA runtime
  # (most of them) never see dxb_gw_install's unconditional dxb_gw_install_runtime_deps call
  # reach apt-get. Fix-G2 tests override this to simulate it missing.
  ldconfig() { echo "libasound.so.2 (libc6,x86-64) => /usr/lib/x86_64-linux-gnu/libasound.so.2"; }
  systemctl() { echo "systemctl $*" >> "$TEST_TMP/calls"; }
  sleep() { :; }
  GW_NEEDS_SETUP=true
  GW_CHANNELS=''
  GW_FAIL_AUTH_SETUP=0
  # GET canned responses a test can override before calling dxb_gw_seed/dxb_gw_seed_igate/etc.
  GW_IGATE_CONFIG='{"id":1,"server":"old.example","enabled":false,"read_only_thing":"keep"}'
  GW_BEACON_CONFIG='{"id":7,"comment":"old","enabled":true}'
}
# Fake curl: records "METHOD PATH BODY" per call and answers from canned responses.
# --data-binary @- means the body was piped over stdin (never as a literal argument); read it.
# -b/-c (cookie jar) only appear on calls that went through dxb_gw_api, distinguishing them from
# dxb_gw_wait_ready's plain readiness ping to the same path. -o FILE means the response is
# written to FILE (curl owns the output file), as dxb_gw_fetch_to uses for the .deb download.
fake_curl() {
  local url='' m=GET data='' a via_api=0 outfile=''
  while (( $# )); do
    case $1 in
      -X) m=$2; shift ;;
      --data|--data-binary) data=$2; shift ;;
      -b|-c) via_api=1 ;;
      -o) outfile=$2; shift ;;
      http*) url=$1 ;;
    esac
    shift
  done
  [[ $data == @-* ]] && data=$(cat)
  if [[ $url == "$DXB_GW_RELEASES"* ]]; then
    local f=$TEST_TMP/http/${url##*/}
    [[ -f $f ]] || return 22
    if [[ -n $outfile ]]; then
      cp "$f" "$outfile"
      echo "FETCH ${url##*/} -> $outfile" >> "$TEST_TMP/calls"
    else
      cat "$f"
    fi
    return 0
  fi
  local p=${url#"$DXB_GW_API"}
  echo "$m $p $data" >> "$TEST_TMP/calls"
  if [[ $p == /auth/setup && $m == GET && $via_api == 1 && $GW_FAIL_AUTH_SETUP == 1 ]]; then
    echo 'not json'
    return 0
  fi
  case "$m $p" in
    "GET /auth/setup")    echo "{\"needs_setup\":$GW_NEEDS_SETUP}" ;;
    "POST /beacons")      echo '{"id":7}' ;;
    "GET /igate/config")  echo "$GW_IGATE_CONFIG" ;;
    "GET /beacons/7")     echo "$GW_BEACON_CONFIG" ;;
    "GET /channels")      echo "${GW_CHANNELS:-[]}" ;;
    "GET /digipeater/rules") echo '[]' ;;
    "PUT /gps")           echo '{}' ;;
    *)                    echo '{}' ;;
  esac
}
gw_cfg() { printf '%s\n' "$@" > "$TEST_TMP/dxberry.txt"; dxb_config_load "$TEST_TMP/dxberry.txt"; dxb_config_validate; }
# named for this file (see the note in tests/test_app_graywolf.sh)
gw_calls() { cat "$TEST_TMP/calls"; }

test_release_base_latest_and_pinned() {
  gw_env
  gw_cfg 'PASSWORD=secretpass'
  assert_eq "$(dxb_gw_release_base)" "http://rel/latest/download"
  gw_cfg 'PASSWORD=secretpass' 'GRAYWOLF_VERSION=v0.14.13'
  assert_eq "$(dxb_gw_release_base)" "http://rel/download/v0.14.13"
}

test_pick_deb_by_architecture() {
  local sums=$'aaa  graywolf_0.14.13_amd64.deb\nbbb  graywolf_0.14.13_arm64.deb\nccc  graywolf_0.14.13_linux_arm64.tar.gz'
  assert_eq "$(printf '%s\n' "$sums" | dxb_gw_pick_deb arm64)" "bbb graywolf_0.14.13_arm64.deb"
  assert_eq "$(printf '%s\n' "$sums" | dxb_gw_pick_deb armhf)" ""
}

test_install_downloads_verifies_and_installs() {
  gw_env
  gw_cfg 'PASSWORD=secretpass'
  echo "deb-bytes" > "$TEST_TMP/http/graywolf_0.14.13_arm64.deb"
  printf '%s  graywolf_0.14.13_arm64.deb\n' "$(sha256sum "$TEST_TMP/http/graywolf_0.14.13_arm64.deb" | cut -d' ' -f1)" > "$TEST_TMP/http/checksums.txt"
  dpkg-query() { return 1; }
  assert_ok dxb_gw_install
  assert_contains "$(gw_calls)" "apt-get install -y"
  assert_contains "$(gw_calls)" "graywolf_0.14.13_arm64.deb"
  assert_contains "$(gw_calls)" "FETCH graywolf_0.14.13_arm64.deb ->"
  assert_eq "${#DXB_FAILED_STEPS[@]}" "0"
}

test_install_rejects_checksum_mismatch_and_missing_release() {
  gw_env
  gw_cfg 'PASSWORD=secretpass'
  echo "deb-bytes" > "$TEST_TMP/http/graywolf_0.14.13_arm64.deb"
  echo "0000000000000000000000000000000000000000000000000000000000000000  graywolf_0.14.13_arm64.deb" > "$TEST_TMP/http/checksums.txt"
  dpkg-query() { return 1; }
  assert_fails dxb_gw_install
  assert_contains "${DXB_FAILED_STEPS[0]}" "checksum mismatch"
  assert_not_contains "$(gw_calls)" "apt-get"
  rm "$TEST_TMP/http/checksums.txt"; DXB_FAILED_STEPS=()
  assert_fails dxb_gw_install
  assert_contains "${DXB_FAILED_STEPS[0]}" "could not download checksums.txt"
}

test_install_skips_when_current() {
  gw_env
  gw_cfg 'PASSWORD=secretpass'
  printf 'x  graywolf_0.14.13_arm64.deb\n' > "$TEST_TMP/http/checksums.txt"
  dpkg-query() { echo "0.14.13"; }
  assert_ok dxb_gw_install
  assert_not_contains "$(gw_calls)" "apt-get"
}

# Measured: `dpkg -s graywolf` has no Depends line, and graywolf-modem (a child process
# graywolf spawns, not a separate unit) needs only libasound.so.2 and crash-loops without it.
test_gw_installs_alsa_when_missing() {
  gw_env
  ldconfig() { :; }
  assert_ok dxb_gw_install_runtime_deps
  assert_contains "$(gw_calls)" "apt-get install -y libasound2t64"
  assert_file_contains "$DXB_LOG_FILE" "installed ALSA runtime for graywolf-modem"
}

test_gw_skips_alsa_when_present() {
  gw_env
  assert_ok dxb_gw_install_runtime_deps
  assert_not_contains "$(gw_calls)" "apt-get"
}

# Trixie's package is libasound2t64 (candidate 1.2.14-1+rpt1); older Debian names it
# libasound2 - try the Trixie name first, then fall back.
test_gw_alsa_falls_back_to_libasound2() {
  gw_env
  ldconfig() { :; }
  apt-get() {
    echo "apt-get $*" >> "$TEST_TMP/calls"
    [[ "$*" == *libasound2t64* ]] && return 100
    return 0
  }
  assert_ok dxb_gw_install_runtime_deps
  assert_contains "$(gw_calls)" "apt-get install -y libasound2t64"
  assert_contains "$(gw_calls)" "apt-get install -y libasound2"
  assert_file_contains "$DXB_LOG_FILE" "installed ALSA runtime for graywolf-modem"
  assert_eq "${#DXB_FAILED_STEPS[@]}" "0"
}

# An offline, already-provisioned box with a crash-looping graywolf-modem must still get healed
# by a plain dxberry-provision, even though the release fetch (GitHub) fails - the ALSA runtime
# check must run before any network access, not after it.
test_gw_alsa_heal_runs_even_when_release_fetch_fails() {
  gw_env
  gw_cfg 'PASSWORD=secretpass'
  ldconfig() { :; }
  # No checksums.txt under $TEST_TMP/http, so the fake curl 404s the release fetch every retry.
  assert_fails dxb_gw_install
  assert_contains "${DXB_FAILED_STEPS[*]}" "could not download checksums.txt"
  assert_contains "$(gw_calls)" "apt-get install -y libasound2t64"
}

# The core graywolf.service works without ALSA (only graywolf-modem needs it), so a failure to
# install the runtime must be reported, not treated as fatal to the rest of dxb_gw_install.
test_gw_alsa_failure_is_reported_but_install_continues() {
  gw_env
  gw_cfg 'PASSWORD=secretpass'
  echo "deb-bytes" > "$TEST_TMP/http/graywolf_0.14.13_arm64.deb"
  printf '%s  graywolf_0.14.13_arm64.deb\n' "$(sha256sum "$TEST_TMP/http/graywolf_0.14.13_arm64.deb" | cut -d' ' -f1)" > "$TEST_TMP/http/checksums.txt"
  dpkg-query() { return 1; }
  ldconfig() { :; }
  apt-get() {
    echo "apt-get $*" >> "$TEST_TMP/calls"
    [[ "$*" == *libasound* ]] && return 1
    return 0
  }
  assert_ok dxb_gw_install
  assert_contains "${DXB_FAILED_STEPS[*]}" "could not install the ALSA runtime"
  assert_contains "$(gw_calls)" "apt-get install -y libasound2t64"
  assert_contains "$(gw_calls)" "apt-get install -y libasound2"
  # Specifically the .deb install line, not just any line mentioning the filename (the fake
  # curl's FETCH log line also names it).
  assert_ok grep -qE '^apt-get install -y .*graywolf_0\.14\.13_arm64\.deb$' "$TEST_TMP/calls"
}

test_seed_fresh_install_creates_admin_station_igate_beacon_digi() {
  gw_env
  gw_cfg 'PASSWORD=secretpass' 'CALLSIGN=N0CALL-2' 'LATITUDE=37.1' 'LONGITUDE=-101.3' 'DIGIPEATER=fillin' 'IGATE_SERVER=noam.aprs2.net' 'BEACON_INTERVAL_MIN=10' 'BEACON_COMMENT=hi'
  assert_ok dxb_gw_seed 0
  local c; c=$(gw_calls)
  assert_contains "$c" 'POST /auth/setup {"username":"admin","password":"secretpass"}'
  assert_contains "$c" 'POST /auth/login {"username":"admin","password":"secretpass"}'
  assert_contains "$c" 'PUT /station/config {"callsign":"N0CALL-2"}'
  assert_contains "$c" '"server":"noam.aprs2.net"'
  assert_contains "$c" '"read_only_thing":"keep"'
  assert_contains "$c" '"gate_rf_to_is":true,"gate_is_to_rf":false'
  assert_contains "$c" 'POST /beacons {"type":"position","latitude":37.1,"longitude":-101.3,"comment":"hi","interval":600,"send_path":"is_only","path":"WIDE1-1,WIDE2-1","symbol_table":"R","symbol":"&","enabled":true}'
  assert_contains "$c" 'PUT /digipeater {"enabled":true,"my_call":"N0CALL-2","dedupe_window_seconds":30}'
  assert_not_contains "$c" "/digipeater/rules {"
  assert_contains "$c" "POST /auth/logout"
  assert_file_contains "$DXB_GW_SEED_STATE" "BEACON_ID=7"
  assert_contains "${DXB_STATUS_LINES[*]}" "digipeater rules pending"
  assert_eq "$(stat -c %a "$DXB_GW_SEED_STATE")" "600"
  assert_contains "$DXB_CONSUMED_SECRETS" "WEBUI_PASSWORD"
}

test_seed_skips_when_already_set_up_unless_reseed() {
  gw_env
  GW_NEEDS_SETUP=false
  gw_cfg 'PASSWORD=secretpass' 'CALLSIGN=N0CALL-2'
  assert_ok dxb_gw_seed 0
  assert_not_contains "$(gw_calls)" "/auth/login"
  assert_ok dxb_gw_seed 1
  assert_contains "$(gw_calls)" "/auth/login"
  assert_contains "$(gw_calls)" "PUT /station/config"
}

test_reseed_updates_existing_beacon_and_creates_rules_when_channel_exists() {
  gw_env
  GW_NEEDS_SETUP=false
  GW_CHANNELS='[{"id":3,"name":"VHF"}]'
  gw_cfg 'PASSWORD=secretpass' 'CALLSIGN=N0CALL-2' 'LATITUDE=37.1' 'LONGITUDE=-101.3' 'DIGIPEATER=wide' 'BEACON_SEND=rf'
  echo "BEACON_ID=7" > "$DXB_GW_SEED_STATE"
  assert_ok dxb_gw_seed 1
  local c; c=$(gw_calls)
  # id is read-only on this endpoint (G1); the merge strips it, even though the path still
  # names the beacon by id.
  assert_contains "$c" 'PUT /beacons/7 {"comment":"DXBerry-Pi iGate","enabled":true,"type":"position"'
  assert_not_contains "$c" '"id":7'
  assert_contains "$c" '"send_path":"rf"'
  assert_contains "$c" '"channel":3'
  assert_contains "$c" 'POST /digipeater/rules {"from_channel":3,"to_channel":3,"alias":"N0CALL-2","alias_type":"exact","max_hops":1,"priority":1,"action":"repeat","enabled":true}'
  assert_contains "$c" 'POST /digipeater/rules {"from_channel":3,"to_channel":3,"alias":"WIDE","alias_type":"widen","max_hops":2,"priority":10,"action":"repeat","enabled":true}'
  assert_file_contains "$DXB_GW_SEED_STATE" "RULES_SEEDED=1"
}

# Graywolf 0.14.13 measured: PUT /igate/config 400s on "unknown field id" when the GET-then-merge
# body echoes id back. id is the only read-only field on that endpoint.
test_seed_igate_strips_read_only_id() {
  gw_env
  GW_IGATE_CONFIG='{"id":1,"enabled":true,"operator_note":"keep"}'
  gw_cfg 'PASSWORD=secretpass' 'CALLSIGN=N0CALL-2' 'IGATE_SERVER=noam.aprs2.net'
  assert_ok dxb_gw_seed 0
  local c; c=$(gw_calls)
  assert_contains "$c" 'PUT /igate/config '
  assert_contains "$c" '"operator_note":"keep"'
  local put_body; put_body=$(sed -n 's/^PUT \/igate\/config //p' "$TEST_TMP/calls")
  assert_not_contains "$put_body" '"id"'
}

# Same read-only-id convention applies to the beacon update endpoint - not yet exercised on
# hardware (no beacon configured on the test Pi), but it is the same API.
test_seed_beacon_update_strips_read_only_id() {
  gw_env
  GW_BEACON_CONFIG='{"id":7,"enabled":true,"operator_note":"keep"}'
  gw_cfg 'PASSWORD=secretpass' 'CALLSIGN=N0CALL-2' 'LATITUDE=37.1' 'LONGITUDE=-101.3'
  echo "BEACON_ID=7" > "$DXB_GW_SEED_STATE"
  assert_ok dxb_gw_seed 0
  assert_contains "$(gw_calls)" 'PUT /beacons/7 '
  local put_body; put_body=$(sed -n 's/^PUT \/beacons\/7 //p' "$TEST_TMP/calls")
  assert_contains "$put_body" '"operator_note":"keep"'
  assert_not_contains "$put_body" '"id"'
}

test_seed_rf_beacon_without_channel_falls_back_to_is_only() {
  gw_env
  gw_cfg 'PASSWORD=secretpass' 'CALLSIGN=N0CALL-2' 'LATITUDE=37.1' 'LONGITUDE=-101.3' 'BEACON_SEND=both'
  assert_ok dxb_gw_seed 0
  assert_contains "$(gw_calls)" '"send_path":"is_only"'
  assert_contains "${DXB_STATUS_LINES[*]}" "APRS-IS only until a radio channel exists"
}

test_seed_without_callsign_only_creates_admin() {
  gw_env
  gw_cfg 'PASSWORD=secretpass'
  assert_ok dxb_gw_seed 0
  assert_contains "$(gw_calls)" "POST /auth/setup"
  assert_not_contains "$(gw_calls)" "/station/config"
  assert_contains "${DXB_STATUS_LINES[*]}" "no CALLSIGN"
}

test_seed_fails_when_auth_setup_query_is_unusable() {
  gw_env
  GW_FAIL_AUTH_SETUP=1
  gw_cfg 'PASSWORD=secretpass' 'CALLSIGN=N0CALL-2'
  assert_fails dxb_gw_seed 0
  assert_contains "${DXB_FAILED_STEPS[*]}" "/auth/setup"
  assert_not_contains "$(gw_calls)" "POST /auth/setup"
  assert_not_contains "$(gw_calls)" "POST /auth/login"
  assert_not_contains "$DXB_CONSUMED_SECRETS" "WEBUI_PASSWORD"
}

# bash writes a "read -p" prompt to stderr and only when input is a terminal, so a 2> /dev/null
# on that read leaves the operator staring at a silent 120 s hang instead of a password prompt.
# The prompt itself needs a pty to exercise; this guards the redirect that broke it.
test_login_prompt_reaches_the_operator() {
  local line
  line=$(grep -n 'read -rst' "$DXB_LIB/graywolf.sh")
  assert_contains "$line" 'Graywolf password for'
  assert_not_contains "$line" '2> /dev/null'
}

test_gw_seed_gps_uses_gpsd_when_enabled_once() {
  gw_env
  printf 'PASSWORD=examplepass\nWEBUI_PASSWORD=hunter2hunter2\n' > "$TEST_TMP/dxberry.txt"
  dxb_config_load "$TEST_TMP/dxberry.txt"; dxb_config_validate
  dxb_gw_seed 0 > /dev/null
  assert_contains "$(cat "$TEST_TMP/calls")" 'PUT /gps {"source":"gpsd","gpsd_host":"localhost","gpsd_port":2947}'
  : > "$TEST_TMP/calls"; GW_NEEDS_SETUP=false
  dxb_gw_seed 0 > /dev/null
  assert_not_contains "$(cat "$TEST_TMP/calls")" 'PUT /gps'
  dxb_gw_seed 1 > /dev/null
  assert_contains "$(cat "$TEST_TMP/calls")" 'PUT /gps'
}
test_gw_seed_gps_skipped_when_none() {
  gw_env
  printf 'PASSWORD=examplepass\nWEBUI_PASSWORD=hunter2hunter2\nGPS_DEVICE=none\n' > "$TEST_TMP/dxberry.txt"
  dxb_config_load "$TEST_TMP/dxberry.txt"; dxb_config_validate
  dxb_gw_seed 0 > /dev/null
  assert_not_contains "$(cat "$TEST_TMP/calls")" '/gps'
}

test_gw_seed_saves_admin_secret() {
  gw_env
  printf 'PASSWORD=examplepass\nWEBUI_PASSWORD=hunter2hunter2\nCALLSIGN=W0BTE\n' > "$TEST_TMP/dxberry.txt"
  dxb_config_load "$TEST_TMP/dxberry.txt"; dxb_config_validate
  dxb_gw_seed 0 > /dev/null
  assert_eq "$(stat -c %a "$DXB_GW_SECRET_FILE")" "600"
  assert_eq "$(cat "$DXB_GW_SECRET_FILE")" $'USER=admin\nPASSWORD=hunter2hunter2'
}

test_gw_login_any_prefers_secret_file_then_config() {
  gw_env
  printf 'USER=admin\nPASSWORD=fromfile12\n' > "$DXB_GW_SECRET_FILE"; chmod 600 "$DXB_GW_SECRET_FILE"
  assert_ok dxb_gw_login_any
  assert_contains "$(cat "$TEST_TMP/calls")" 'POST /auth/login {"username":"admin","password":"fromfile12"}'
  rm -f "$DXB_GW_SECRET_FILE"; : > "$TEST_TMP/calls"
  printf 'PASSWORD=examplepass\nWEBUI_PASSWORD=fromconfig1\n' > "$TEST_TMP/dxberry.txt"
  dxb_config_load "$TEST_TMP/dxberry.txt"; dxb_config_validate
  assert_ok dxb_gw_login_any
  assert_contains "$(cat "$TEST_TMP/calls")" '"password":"fromconfig1"'
  assert_eq "$(cat "$DXB_GW_SECRET_FILE")" $'USER=admin\nPASSWORD=fromconfig1'   # a successful config login is saved too
}

test_seed_login_prompt_without_terminal_fails_distinctly() {
  gw_env
  GW_NEEDS_SETUP=false
  gw_cfg 'PASSWORD=secretpass' 'WEBUI_PASSWORD=<applied>' 'CALLSIGN=N0CALL-2'
  local saved_tty=$DXB_TTY
  DXB_TTY=/dev/null
  assert_fails dxb_gw_seed 1 2> /dev/null
  DXB_TTY=$saved_tty
  assert_contains "${DXB_FAILED_STEPS[*]}" "no terminal is available"
  assert_not_contains "$(gw_calls)" "/auth/login"
  assert_not_contains "$DXB_CONSUMED_SECRETS" "WEBUI_PASSWORD"
}
