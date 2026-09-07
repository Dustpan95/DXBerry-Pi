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

# dxb_net_render_iface eth0|wlan0: print the ifupdown stanza for the configured mode.
dxb_net_render_iface() {
  local iface=$1 method addr gw
  if [[ ${DXB_CFG[_MODE]} == static ]]; then
    method=static; addr="address ${DXB_CFG[STATIC_IP]}"; gw="gateway ${DXB_CFG[GATEWAY]}"
  else
    method=dhcp; addr=''; gw=''
  fi
  dxb_render "$DXB_TEMPLATES/interfaces-$iface.tmpl" "METHOD=$method" "ADDRESS=$addr" "GATEWAY=$gw"
}

# Import WiFi credentials through DietPi's own tool so its WiFi menu keeps working.
dxb_net_import_wifi() {
  [[ ${DXB_CFG[WIFI_PASSWORD]} == "$DXB_APPLIED" ]] && return 0
  dxb_net_write_wifi_txt "$DXB_DIETPI_WIFI" "${DXB_CFG[WIFI_SSID]}" "${DXB_CFG[WIFI_PASSWORD]}"
  if [[ -x $DXB_DIETPI_WIFIDB ]]; then
    if "$DXB_DIETPI_WIFIDB" 1 > /dev/null 2>&1; then
      dxb_info "WiFi credentials imported for ${DXB_CFG[WIFI_SSID]}"
    else
      dxb_step_failed network "dietpi-wifidb failed to import WiFi credentials"
      return 1
    fi
  else
    dxb_step_failed network "$DXB_DIETPI_WIFIDB not found; WiFi credentials not imported"
    return 1
  fi
}

dxb_net_install_netwatch() {
  local unit="$DXB_SYSTEMD_DIR/dxberry-netwatch.service" content
  content=$(< "$DXB_TEMPLATES/dxberry-netwatch.service")
  if dxb_write_if_changed "$unit" "$content"; then
    systemctl daemon-reload
    systemctl enable dxberry-netwatch > /dev/null 2>&1
    dxb_info "dxberry-netwatch unit installed"
    return 0
  fi
  return 1
}

provision_network() {
  local content
  mkdir -p "$DXB_IFACES_DIR"
  content=$(dxb_net_render_iface eth0) || { dxb_step_failed network "eth0 template failed"; return 1; }
  dxb_write_if_changed "$DXB_IFACES_DIR/eth0.conf" "$content" && { DXB_NET_CHANGED=1; dxb_info "wrote eth0.conf (${DXB_CFG[_MODE]})"; }
  if (( DXB_CFG[_WIFI] )); then
    content=$(dxb_net_render_iface wlan0) || { dxb_step_failed network "wlan0 template failed"; return 1; }
    dxb_write_if_changed "$DXB_IFACES_DIR/wlan0.conf" "$content" && { DXB_NET_CHANGED=1; dxb_info "wrote wlan0.conf"; }
    dxb_net_import_wifi
  elif [[ -f $DXB_IFACES_DIR/wlan0.conf ]]; then
    rm -f "$DXB_IFACES_DIR/wlan0.conf"
    # shellcheck disable=SC2034
    DXB_NET_CHANGED=1
    dxb_info "removed wlan0.conf (no WIFI_SSID)"
  fi
  if [[ ${DXB_CFG[_MODE]} == static ]]; then
    # shellcheck disable=SC2086
    content=$(printf 'nameserver %s\n' ${DXB_CFG[DNS]})
    dxb_write_if_changed "$DXB_RESOLV_CONF" "$content" && dxb_info "wrote resolv.conf"
  fi
  dxb_net_install_netwatch || true
  if (( DXB_CFG[_WIFI] )); then dxb_status_add "network: ${DXB_CFG[_MODE]} ${DXB_CFG[STATIC_IP]:-} - eth0 primary, wlan0 (${DXB_CFG[WIFI_SSID]}) failover"
  else dxb_status_add "network: ${DXB_CFG[_MODE]} ${DXB_CFG[STATIC_IP]:-} - eth0 only"; fi
  return 0
}
