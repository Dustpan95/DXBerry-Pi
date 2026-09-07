#!/usr/bin/env bash
# shellcheck disable=SC1091
source "$DXB_LIB/common.sh"
source "$DXB_LIB/config.sh"
source "$DXB_LIB/network.sh"

net_env() {
  export DXB_IFACES_DIR=$TEST_TMP/ifaces DXB_RESOLV_CONF=$TEST_TMP/resolv.conf DXB_SYSTEMD_DIR=$TEST_TMP/systemd \
    DXB_DIETPI_WIFIDB=$TEST_TMP/wifidb DXB_DIETPI_WIFI=$TEST_TMP/dietpi-wifi.txt DXB_LOG_FILE=$TEST_TMP/log DXB_ZONEINFO_DIR=$TEST_TMP/nozone
  mkdir -p "$DXB_SYSTEMD_DIR"
  printf '#!/bin/bash\necho "wifidb $*" >> %s/calls\n' "$TEST_TMP" > "$DXB_DIETPI_WIFIDB"; chmod +x "$DXB_DIETPI_WIFIDB"
  printf "aWIFI_SSID[0]=''\naWIFI_KEY[0]=''\naWIFI_KEYMGR[0]='WPA-PSK'\n" > "$DXB_DIETPI_WIFI"
  # shellcheck disable=SC2317
  systemctl() { echo "systemctl $*" >> "$TEST_TMP/calls"; }
  : > "$TEST_TMP/calls"
  # shellcheck disable=SC2034
  DXB_STATUS_LINES=()
  # shellcheck disable=SC2034
  DXB_FAILED_STEPS=()
  # shellcheck disable=SC2034
  DXB_NET_CHANGED=0
}
net_cfg() { printf '%s\n' "$@" > "$TEST_TMP/dxberry.txt"; dxb_config_load "$TEST_TMP/dxberry.txt"; dxb_config_validate; }

test_render_static_and_dhcp_stanzas() {
  net_env
  net_cfg 'PASSWORD=secretpass' 'STATIC_IP=192.168.1.90/24' 'GATEWAY=192.168.1.1' 'WIFI_SSID=Home' 'WIFI_PASSWORD=wifipass1' 'WIFI_COUNTRY=US'
  local out; out=$(dxb_net_render_iface eth0)
  assert_contains "$out" $'iface eth0 inet static\naddress 192.168.1.90/24\ngateway 192.168.1.1'
  assert_not_contains "$out" "allow-hotplug"
  out=$(dxb_net_render_iface wlan0)
  assert_contains "$out" $'iface wlan0 inet static\naddress 192.168.1.90/24\ngateway 192.168.1.1\nwpa-conf /etc/wpa_supplicant/wpa_supplicant.conf'
  net_cfg 'PASSWORD=secretpass'
  out=$(dxb_net_render_iface eth0)
  assert_contains "$out" "iface eth0 inet dhcp"
  assert_not_contains "$out" "address"
}

test_provision_network_static_with_wifi_writes_everything_once() {
  net_env
  net_cfg 'PASSWORD=secretpass' 'STATIC_IP=192.168.1.90/24' 'GATEWAY=192.168.1.1' 'DNS=192.168.1.1 1.1.1.1' 'WIFI_SSID=Home' 'WIFI_PASSWORD=wifipass1' 'WIFI_COUNTRY=US'
  DXB_MODE=first-boot provision_network
  assert_eq "$DXB_NET_CHANGED" "1"
  assert_file_contains "$DXB_IFACES_DIR/eth0.conf" "address 192.168.1.90/24"
  assert_file_contains "$DXB_IFACES_DIR/wlan0.conf" "wpa-conf"
  assert_eq "$(cat "$DXB_RESOLV_CONF")" $'nameserver 192.168.1.1\nnameserver 1.1.1.1'
  assert_file_contains "$DXB_DIETPI_WIFI" "aWIFI_SSID[0]='Home'"
  assert_file_contains "$TEST_TMP/calls" "wifidb 1"
  assert_file_contains "$DXB_SYSTEMD_DIR/dxberry-netwatch.service" "ExecStart=/opt/dxberry/bin/dxberry-netwatch run"
  assert_file_contains "$TEST_TMP/calls" "systemctl enable dxberry-netwatch"
  DXB_NET_CHANGED=0; : > "$TEST_TMP/calls"
  # shellcheck disable=SC2034
  DXB_CFG[WIFI_PASSWORD]=$DXB_APPLIED   # what provision_scrub does after the first run
  DXB_MODE=run provision_network
  assert_eq "$DXB_NET_CHANGED" "0"
  assert_file_not_contains "$TEST_TMP/calls" "wifidb 1"
}

test_provision_network_applied_wifi_password_skips_import() {
  net_env
  net_cfg 'PASSWORD=secretpass' 'WIFI_SSID=Home' 'WIFI_PASSWORD=<applied>' 'WIFI_COUNTRY=US'
  DXB_MODE=run provision_network
  assert_file_contains "$DXB_DIETPI_WIFI" "aWIFI_SSID[0]=''"
  assert_file_not_contains "$TEST_TMP/calls" "wifidb 1"
  assert_file_contains "$DXB_IFACES_DIR/eth0.conf" "iface eth0 inet dhcp"
  [[ -f $DXB_RESOLV_CONF ]] && _fail "resolv.conf must not be written in DHCP mode"
}

test_provision_network_removes_wlan0_when_wifi_unset() {
  net_env
  mkdir -p "$DXB_IFACES_DIR"; echo old > "$DXB_IFACES_DIR/wlan0.conf"
  net_cfg 'PASSWORD=secretpass'
  DXB_MODE=run provision_network
  [[ -f $DXB_IFACES_DIR/wlan0.conf ]] && _fail "wlan0.conf should be removed"
  assert_eq "$DXB_NET_CHANGED" "1"
  # Missing templates should fail gracefully
  local saved_templates=$DXB_TEMPLATES
  rm -rf "$DXB_IFACES_DIR"
  net_cfg 'PASSWORD=secretpass'
  DXB_TEMPLATES=$TEST_TMP/empty-templates
  mkdir -p "$DXB_TEMPLATES"
  assert_fails provision_network 2> /dev/null
  assert_file_contains "$TEST_TMP/log" "eth0 template failed"
  [[ -f $DXB_IFACES_DIR/eth0.conf ]] && _fail "eth0.conf should not be created when template is missing"
  DXB_TEMPLATES=$saved_templates
}
