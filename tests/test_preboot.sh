#!/usr/bin/env bash
# shellcheck disable=SC1091
source "$DXB_LIB/common.sh"
source "$DXB_LIB/network.sh"
source "$DXB_ROOT/provision/bin/dxberry-preboot"

preboot_env() {
  export DXB_BOOT_DIR=$TEST_TMP/bootfs DXB_DIETPI_TXT=$TEST_TMP/dietpi.txt DXB_DIETPI_WIFI=$TEST_TMP/dietpi-wifi.txt \
    DXB_STATE_DIR=$TEST_TMP/state DXB_LOG_FILE=$TEST_TMP/state/log DXB_ZONEINFO_DIR=$TEST_TMP/nozone
  mkdir -p "$DXB_BOOT_DIR"
  printf 'AUTO_SETUP_NET_HOSTNAME=DietPi\nAUTO_SETUP_GLOBAL_PASSWORD=dietpi\nAUTO_SETUP_TIMEZONE=UTC\nAUTO_SETUP_NET_ETHERNET_ENABLED=1\nAUTO_SETUP_NET_WIFI_ENABLED=0\nAUTO_SETUP_NET_WIFI_COUNTRY_CODE=GB\nAUTO_SETUP_NET_USESTATIC=0\nAUTO_SETUP_NET_STATIC_IP=192.168.0.100\nAUTO_SETUP_NET_STATIC_MASK=255.255.255.0\nAUTO_SETUP_NET_STATIC_GATEWAY=192.168.0.1\nAUTO_SETUP_NET_STATIC_DNS=9.9.9.9 149.112.112.112\nCONFIG_SERIAL_CONSOLE_ENABLE=0\n' > "$DXB_DIETPI_TXT"
  printf "aWIFI_SSID[0]=''\naWIFI_KEY[0]=''\naWIFI_KEYMGR[0]='WPA-PSK'\naWIFI_SSID[1]=''\n" > "$DXB_DIETPI_WIFI"
}

test_preboot_writes_dietpi_settings_from_config() {
  preboot_env
  printf 'HOSTNAME=station\nPASSWORD=secretpass\nTIMEZONE=America/Chicago\nSTATIC_IP=192.168.1.90/24\nGATEWAY=192.168.1.1\nDNS=192.168.1.1 1.1.1.1\nWIFI_SSID=Home\nWIFI_PASSWORD=wifipass1\nWIFI_COUNTRY=US\nSERIAL_CONSOLE=on\n' > "$DXB_BOOT_DIR/dxberry.txt"
  assert_ok main
  assert_file_contains "$DXB_DIETPI_TXT" "AUTO_SETUP_NET_HOSTNAME=station"
  assert_file_contains "$DXB_DIETPI_TXT" "AUTO_SETUP_GLOBAL_PASSWORD=secretpass"
  assert_file_contains "$DXB_DIETPI_TXT" "AUTO_SETUP_TIMEZONE=America/Chicago"
  assert_file_contains "$DXB_DIETPI_TXT" "AUTO_SETUP_NET_USESTATIC=1"
  assert_file_contains "$DXB_DIETPI_TXT" "AUTO_SETUP_NET_STATIC_IP=192.168.1.90"
  assert_file_contains "$DXB_DIETPI_TXT" "AUTO_SETUP_NET_STATIC_MASK=255.255.255.0"
  assert_file_not_contains "$DXB_DIETPI_TXT" "192.168.1.90/24"
  assert_file_contains "$DXB_DIETPI_TXT" "AUTO_SETUP_NET_STATIC_GATEWAY=192.168.1.1"
  assert_file_contains "$DXB_DIETPI_TXT" "AUTO_SETUP_NET_STATIC_DNS=192.168.1.1 1.1.1.1"
  assert_file_contains "$DXB_DIETPI_TXT" "AUTO_SETUP_NET_WIFI_ENABLED=0"
  assert_file_contains "$DXB_DIETPI_TXT" "AUTO_SETUP_NET_WIFI_COUNTRY_CODE=US"
  assert_file_contains "$DXB_DIETPI_TXT" "CONFIG_SERIAL_CONSOLE_ENABLE=1"
  assert_file_contains "$DXB_DIETPI_WIFI" "aWIFI_SSID[0]='Home'"
  assert_file_contains "$DXB_DIETPI_WIFI" "aWIFI_KEY[0]='wifipass1'"
  assert_file_contains "$DXB_DIETPI_WIFI" "aWIFI_SSID[1]=''"
  [[ -f $DXB_BOOT_DIR/dxberry-ERROR.txt ]] && _fail "no error file expected"
  [[ -f $DXB_STATE_DIR/preboot.done ]] || _fail "marker missing"
}

test_preboot_dhcp_and_no_wifi_leaves_wifi_file_alone() {
  preboot_env
  printf 'PASSWORD=secretpass\n' > "$DXB_BOOT_DIR/dxberry.txt"
  assert_ok main
  assert_file_contains "$DXB_DIETPI_TXT" "AUTO_SETUP_NET_USESTATIC=0"
  assert_file_contains "$DXB_DIETPI_TXT" "AUTO_SETUP_NET_HOSTNAME=dxberry-pi"
  assert_file_contains "$DXB_DIETPI_WIFI" "aWIFI_SSID[0]=''"
}

test_preboot_applied_password_is_not_written() {
  preboot_env
  printf 'PASSWORD=<applied>\n' > "$DXB_BOOT_DIR/dxberry.txt"
  assert_ok main
  assert_file_contains "$DXB_DIETPI_TXT" "AUTO_SETUP_GLOBAL_PASSWORD=dietpi"
}

test_preboot_invalid_config_writes_error_file_and_leaves_dietpi_txt() {
  preboot_env
  printf 'HOSTNAME=Bad\nSTATIC_IP=nope\n' > "$DXB_BOOT_DIR/dxberry.txt"
  assert_ok main
  assert_file_contains "$DXB_BOOT_DIR/dxberry-ERROR.txt" "PASSWORD is required"
  assert_file_contains "$DXB_BOOT_DIR/dxberry-ERROR.txt" "line 1: HOSTNAME must be"
  assert_file_contains "$DXB_BOOT_DIR/dxberry-ERROR.txt" "line 2: STATIC_IP must be"
  assert_file_contains "$DXB_DIETPI_TXT" "AUTO_SETUP_NET_HOSTNAME=DietPi"
}

test_preboot_missing_config_writes_hint() {
  preboot_env
  assert_ok main
  assert_file_contains "$DXB_BOOT_DIR/dxberry-ERROR.txt" "Rename dxberry.txt.example to dxberry.txt"
}

test_preboot_writes_gps_boot_lines_but_never_the_ntp_mode() {
  preboot_env
  printf 'PASSWORD=examplepass\nGPS_DEVICE=uart\nGPS_PPS=18\n' > "$DXB_BOOT_DIR/dxberry.txt"
  : > "$DXB_BOOT_DIR/config.txt"
  main
  # CONFIG_NTP_MODE=0 belongs to provision_radio: before DietPi's first run it would leave a
  # PI with no RTC on a stale clock through apt and the Graywolf TLS download.
  assert_file_not_contains "$DXB_DIETPI_TXT" "CONFIG_NTP_MODE"
  assert_file_contains "$DXB_BOOT_DIR/config.txt" "enable_uart=1"
  assert_file_contains "$DXB_BOOT_DIR/config.txt" "dtoverlay=disable-bt"
  assert_file_contains "$DXB_BOOT_DIR/config.txt" "dtoverlay=pps-gpio,gpiopin=18"
}

test_preboot_auto_gps_leaves_config_txt_alone() {
  preboot_env
  printf 'PASSWORD=examplepass\n' > "$DXB_BOOT_DIR/dxberry.txt"
  printf 'arm_64bit=1\n' > "$DXB_BOOT_DIR/config.txt"
  main
  assert_eq "$(cat "$DXB_BOOT_DIR/config.txt")" "arm_64bit=1"
  assert_file_not_contains "$DXB_DIETPI_TXT" "CONFIG_NTP_MODE"
}
