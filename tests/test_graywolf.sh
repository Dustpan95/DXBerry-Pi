#!/usr/bin/env bash
# shellcheck disable=SC1091,SC2034,SC2317
source "$DXB_LIB/common.sh"
source "$DXB_LIB/config.sh"
source "$DXB_LIB/graywolf.sh"

gw_env() {
  export DXB_STATE_DIR=$TEST_TMP/state DXB_LOG_FILE=$TEST_TMP/state/log DXB_GW_COOKIES=$TEST_TMP/cookies \
    DXB_GW_SEED_STATE=$TEST_TMP/state/graywolf-seed.env DXB_GW_API=http://gw/api DXB_GW_RELEASES=http://rel DXB_DPKG_ARCH=arm64 DXB_ZONEINFO_DIR=$TEST_TMP/nozone
  mkdir -p "$DXB_STATE_DIR" "$TEST_TMP/http"
  : > "$TEST_TMP/calls"
  DXB_STATUS_LINES=(); DXB_FAILED_STEPS=(); DXB_CONSUMED_SECRETS=''
  DXB_CURL=fake_curl
  apt-get() { echo "apt-get $*" >> "$TEST_TMP/calls"; }
  systemctl() { echo "systemctl $*" >> "$TEST_TMP/calls"; }
  sleep() { :; }
  GW_NEEDS_SETUP=true
  GW_CHANNELS=''
  GW_FAIL_AUTH_SETUP=0
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
    "GET /igate/config")  echo '{"id":1,"server":"old.example","enabled":false,"read_only_thing":"keep"}' ;;
    "GET /beacons/7")     echo '{"id":7,"comment":"old","enabled":true}' ;;
    "GET /channels")      echo "${GW_CHANNELS:-[]}" ;;
    "GET /digipeater/rules") echo '[]' ;;
    *)                    echo '{}' ;;
  esac
}
gw_cfg() { printf '%s\n' "$@" > "$TEST_TMP/dxberry.txt"; dxb_config_load "$TEST_TMP/dxberry.txt"; dxb_config_validate; }
calls() { cat "$TEST_TMP/calls"; }

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
  assert_contains "$(calls)" "apt-get install -y"
  assert_contains "$(calls)" "graywolf_0.14.13_arm64.deb"
  assert_contains "$(calls)" "FETCH graywolf_0.14.13_arm64.deb ->"
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
  assert_not_contains "$(calls)" "apt-get"
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
  assert_not_contains "$(calls)" "apt-get"
}

test_seed_fresh_install_creates_admin_station_igate_beacon_digi() {
  gw_env
  gw_cfg 'PASSWORD=secretpass' 'CALLSIGN=N0CALL-2' 'LATITUDE=37.1' 'LONGITUDE=-101.3' 'DIGIPEATER=fillin' 'IGATE_SERVER=noam.aprs2.net' 'BEACON_INTERVAL_MIN=10' 'BEACON_COMMENT=hi'
  assert_ok dxb_gw_seed 0
  local c; c=$(calls)
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
  assert_not_contains "$(calls)" "/auth/login"
  assert_ok dxb_gw_seed 1
  assert_contains "$(calls)" "/auth/login"
  assert_contains "$(calls)" "PUT /station/config"
}

test_reseed_updates_existing_beacon_and_creates_rules_when_channel_exists() {
  gw_env
  GW_NEEDS_SETUP=false
  GW_CHANNELS='[{"id":3,"name":"VHF"}]'
  gw_cfg 'PASSWORD=secretpass' 'CALLSIGN=N0CALL-2' 'LATITUDE=37.1' 'LONGITUDE=-101.3' 'DIGIPEATER=wide' 'BEACON_SEND=rf'
  echo "BEACON_ID=7" > "$DXB_GW_SEED_STATE"
  assert_ok dxb_gw_seed 1
  local c; c=$(calls)
  assert_contains "$c" 'PUT /beacons/7 {"id":7,"comment":"DXBerry-Pi iGate","enabled":true,"type":"position"'
  assert_contains "$c" '"send_path":"rf"'
  assert_contains "$c" '"channel":3'
  assert_contains "$c" 'POST /digipeater/rules {"from_channel":3,"to_channel":3,"alias":"N0CALL-2","alias_type":"exact","max_hops":1,"priority":1,"action":"repeat","enabled":true}'
  assert_contains "$c" 'POST /digipeater/rules {"from_channel":3,"to_channel":3,"alias":"WIDE","alias_type":"widen","max_hops":2,"priority":10,"action":"repeat","enabled":true}'
  assert_file_contains "$DXB_GW_SEED_STATE" "RULES_SEEDED=1"
}

test_seed_rf_beacon_without_channel_falls_back_to_is_only() {
  gw_env
  gw_cfg 'PASSWORD=secretpass' 'CALLSIGN=N0CALL-2' 'LATITUDE=37.1' 'LONGITUDE=-101.3' 'BEACON_SEND=both'
  assert_ok dxb_gw_seed 0
  assert_contains "$(calls)" '"send_path":"is_only"'
  assert_contains "${DXB_STATUS_LINES[*]}" "APRS-IS only until a radio channel exists"
}

test_seed_without_callsign_only_creates_admin() {
  gw_env
  gw_cfg 'PASSWORD=secretpass'
  assert_ok dxb_gw_seed 0
  assert_contains "$(calls)" "POST /auth/setup"
  assert_not_contains "$(calls)" "/station/config"
  assert_contains "${DXB_STATUS_LINES[*]}" "no CALLSIGN"
}

test_seed_fails_when_auth_setup_query_is_unusable() {
  gw_env
  GW_FAIL_AUTH_SETUP=1
  gw_cfg 'PASSWORD=secretpass' 'CALLSIGN=N0CALL-2'
  assert_fails dxb_gw_seed 0
  assert_contains "${DXB_FAILED_STEPS[*]}" "/auth/setup"
  assert_not_contains "$(calls)" "POST /auth/setup"
  assert_not_contains "$(calls)" "POST /auth/login"
  assert_not_contains "$DXB_CONSUMED_SECRETS" "WEBUI_PASSWORD"
}

test_seed_login_prompt_without_terminal_fails_distinctly() {
  gw_env
  GW_NEEDS_SETUP=false
  gw_cfg 'PASSWORD=secretpass' 'WEBUI_PASSWORD=<applied>' 'CALLSIGN=N0CALL-2'
  local saved_tty=$DXB_TTY
  DXB_TTY=/dev/null
  assert_fails dxb_gw_seed 1
  DXB_TTY=$saved_tty
  assert_contains "${DXB_FAILED_STEPS[*]}" "no terminal is available"
  assert_not_contains "$(calls)" "/auth/login"
  assert_not_contains "$DXB_CONSUMED_SECRETS" "WEBUI_PASSWORD"
}
