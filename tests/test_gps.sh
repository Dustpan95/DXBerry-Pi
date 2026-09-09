#!/usr/bin/env bash
# shellcheck disable=SC1091,SC2034,SC2317
source "$DXB_LIB/common.sh"
source "$DXB_LIB/config.sh"
source "$DXB_LIB/gps.sh"

gps_env() {
  export DXB_STATE_DIR=$TEST_TMP/state DXB_LOG_FILE=$TEST_TMP/state/log DXB_GPSD_DEFAULT=$TEST_TMP/etc/default/gpsd \
    DXB_CHRONY_DROPIN=$TEST_TMP/etc/chrony/conf.d/dxberry.conf DXB_GPSPIPE=fake_gpspipe DXB_ZONEINFO_DIR=$TEST_TMP/nozone \
    DXB_RPI_CONFIG_TXT=$TEST_TMP/boot/config.txt
  mkdir -p "$DXB_STATE_DIR" "$TEST_TMP/boot"; : > "$TEST_TMP/calls"; : > "$DXB_RPI_CONFIG_TXT"
  systemctl() { echo "systemctl $*" >> "$TEST_TMP/calls"; }
  timeout() { shift; "$@"; }
  fake_gpspipe() { cat "$TEST_TMP/gpspipe.out" 2> /dev/null; }
  DXB_STATUS_LINES=(); DXB_FAILED_STEPS=()
}
gps_cfg() { printf 'PASSWORD=examplepass\n%s\n' "$@" > "$TEST_TMP/dxberry.txt"; dxb_config_load "$TEST_TMP/dxberry.txt"; dxb_config_validate; }

test_gpsd_default_shapes() {
  gps_env
  gps_cfg ''; assert_eq "$(dxb_gps_gpsd_default | grep -E '^(START_DAEMON|DEVICES)=')" $'START_DAEMON="true"\nDEVICES=""'
  gps_cfg 'GPS_DEVICE=uart' 'GPS_PPS=18'; assert_eq "$(dxb_gps_gpsd_default | grep '^DEVICES=')" 'DEVICES="/dev/ttyAMA0 /dev/pps0"'
  gps_cfg 'GPS_DEVICE=none'; assert_eq "$(dxb_gps_gpsd_default | grep '^START_DAEMON=')" 'START_DAEMON="false"'
}

test_chrony_conf_noselect_only_with_pps() {
  gps_env
  gps_cfg ''; assert_eq "$(dxb_gps_chrony_conf | grep 'SHM 0')" "refclock SHM 0 refid GPS precision 1e-1 offset 0.2 delay 0.2"
  gps_cfg 'GPS_PPS=18'; assert_eq "$(dxb_gps_chrony_conf | grep 'SHM 0')" "refclock SHM 0 refid GPS precision 1e-1 offset 0.2 delay 0.2 noselect"
}

test_gps_configure_writes_enables_restarts_once() {
  gps_env; gps_cfg 'GPS_DEVICE=uart'
  assert_ok dxb_gps_configure
  assert_file_contains "$DXB_GPSD_DEFAULT" 'DEVICES="/dev/ttyAMA0"'
  assert_file_contains "$DXB_CHRONY_DROPIN" "refclock SHM 1"
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl enable gpsd.socket"
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl restart gpsd"
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl restart chrony"
  : > "$TEST_TMP/calls"
  assert_ok dxb_gps_configure
  assert_not_contains "$(cat "$TEST_TMP/calls")" "restart"
  gps_cfg 'GPS_DEVICE=none'
  assert_ok dxb_gps_configure
  assert_contains "$(cat "$TEST_TMP/calls")" "systemctl disable --now gpsd.socket gpsd"
  [[ -f $DXB_CHRONY_DROPIN ]] && _fail "chrony drop-in should be removed when GPS is off"
}

test_gps_boot_config_lines() {
  gps_env; gps_cfg 'GPS_DEVICE=uart' 'GPS_PPS=4'
  assert_ok dxb_gps_boot_config
  assert_eq "$(cat "$DXB_RPI_CONFIG_TXT")" $'enable_uart=1\ndtoverlay=disable-bt\ndtoverlay=pps-gpio,gpiopin=4'
  dxb_gps_boot_config; assert_eq "$?" "1"
}

test_maidenhead() {
  assert_eq "$(dxb_maidenhead 37.145833 -101.375)" "DM97hd"
  assert_eq "$(dxb_maidenhead 51.5 -0.1)" "IO91wm"
  assert_eq "$(dxb_maidenhead -33.8688 151.2093)" "QF56od"
}

test_gps_fix_parses_tpv_and_sky() {
  gps_env
  cat > "$TEST_TMP/gpspipe.out" <<'EOF'
{"class":"VERSION","release":"3.25"}
{"class":"TPV","mode":3,"time":"2026-09-09T02:00:00.000Z","lat":37.145833,"lon":-101.375,"altHAE":1000.0,"speed":2.0}
{"class":"SKY","nSat":12,"uSat":8}
EOF
  local j; j=$(dxb_gps_fix)
  assert_eq "$(jq -r '.fix' <<< "$j")" "3"
  assert_eq "$(jq -r '.grid' <<< "$j")" "DM97hd"
  assert_eq "$(jq -r '.alt_ft' <<< "$j")" "3281"
  assert_eq "$(jq -r '.speed_mph' <<< "$j")" "4.5"
  assert_eq "$(jq -r '.sats_used, .sats_seen' <<< "$j" | tr '\n' ' ')" "8 12 "
  rm -f "$TEST_TMP/gpspipe.out"
  assert_eq "$(dxb_gps_fix)" '{"fix":0}'
  assert_eq "$(dxb_gps_status_line)" "gps: no fix"
}
