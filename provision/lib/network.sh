#!/bin/bash
# shellcheck shell=bash
# Network provisioning: interfaces files, resolv.conf, WiFi credentials, dxberry-netwatch.

: "${DXB_IFACES_DIR:=/etc/network/interfaces.d}"
: "${DXB_RESOLV_CONF:=/etc/resolv.conf}"
: "${DXB_TEMPLATES:=/opt/dxberry/templates}"
: "${DXB_SYSTEMD_DIR:=/etc/systemd/system}"
: "${DXB_DIETPI_WIFIDB:=/boot/dietpi/func/dietpi-wifidb}"
: "${DXB_DIETPI_WIFI:=/boot/dietpi-wifi.txt}"
# shellcheck disable=SC2034
DXB_NET_CHANGED=0

# dxb_net_write_wifi_txt FILE SSID KEY: fill slot 0 of a DietPi dietpi-wifi.txt.
dxb_net_write_wifi_txt() {
  local file=$1 ssid=$2 key=$3
  dxb_set_kv "$file" 'aWIFI_SSID[0]' "$(dxb_squote "$ssid")"
  dxb_set_kv "$file" 'aWIFI_KEY[0]' "$(dxb_squote "$key")"
  dxb_set_kv "$file" 'aWIFI_KEYMGR[0]' "'WPA-PSK'"
}
