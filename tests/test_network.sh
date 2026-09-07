#!/usr/bin/env bash
# shellcheck disable=SC1091
source "$DXB_LIB/common.sh"
source "$DXB_LIB/config.sh"
source "$DXB_LIB/network.sh"

net_env() {
  export DXB_IFACES_DIR=$TEST_TMP/ifaces DXB_RESOLV_CONF=$TEST_TMP/resolv.conf DXB_SYSTEMD_DIR=$TEST_TMP/systemd \
    DXB_DIETPI_WIFIDB=$TEST_TMP/wifidb DXB_DIETPI_WIFI=$TEST_TMP/dietpi-wifi.txt DXB_LOG_FILE=$TEST_TMP/log DXB_ZONEINFO_DIR=$TEST_TMP/nozone \
    DXB_DIETPI_SET_HW=$TEST_TMP/set_hw DXB_SYS_NET=$TEST_TMP/sys DXB_INTERFACES_FILE=$TEST_TMP/interfaces
  mkdir -p "$DXB_SYSTEMD_DIR" "$DXB_SYS_NET/eth0" "$DXB_SYS_NET/wlan0"
  printf '#!/bin/bash\necho "wifidb $*" >> %s/calls\n' "$TEST_TMP" > "$DXB_DIETPI_WIFIDB"; chmod +x "$DXB_DIETPI_WIFIDB"
  printf '#!/bin/bash\necho "set_hw $*" >> %s/calls\n' "$TEST_TMP" > "$DXB_DIETPI_SET_HW"; chmod +x "$DXB_DIETPI_SET_HW"
  printf 'source /etc/network/interfaces.d/*\nauto lo\niface lo inet loopback\n' > "$DXB_INTERFACES_FILE"
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
  DXB_NET_STRAY_REPORTED=0
  DXB_CONSUMED_SECRETS=''
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
  assert_file_contains "$TEST_TMP/calls" "systemctl daemon-reload"
  assert_file_contains "$TEST_TMP/calls" "systemctl enable dxberry-netwatch"
  DXB_NET_CHANGED=0; : > "$TEST_TMP/calls"
  # shellcheck disable=SC2034
  DXB_CFG[WIFI_PASSWORD]=$DXB_APPLIED   # what provision_scrub does after the first run
  DXB_MODE=run provision_network
  assert_eq "$DXB_NET_CHANGED" "0"
  assert_file_not_contains "$TEST_TMP/calls" "wifidb 1"
  # An unchanged unit must still be re-enabled (idempotent), but must not force a daemon-reload.
  assert_file_contains "$TEST_TMP/calls" "systemctl enable dxberry-netwatch"
  assert_file_not_contains "$TEST_TMP/calls" "systemctl daemon-reload"
  # WiFi import failure should not prevent other steps; provision_network still succeeds
  net_env
  net_cfg 'PASSWORD=secretpass' 'STATIC_IP=192.168.1.90/24' 'GATEWAY=192.168.1.1' 'WIFI_SSID=Home' 'WIFI_PASSWORD=wifipass1' 'WIFI_COUNTRY=US'
  printf '#!/bin/bash\nexit 1\n' > "$DXB_DIETPI_WIFIDB"; chmod +x "$DXB_DIETPI_WIFIDB"
  # shellcheck disable=SC2034
  DXB_MODE=first-boot
  assert_ok provision_network 2> /dev/null
  assert_file_contains "$DXB_IFACES_DIR/eth0.conf" "address 192.168.1.90/24"
  assert_file_contains "$DXB_SYSTEMD_DIR/dxberry-netwatch.service" "ExecStart"
  assert_contains "${DXB_FAILED_STEPS[*]}" "dietpi-wifidb"
  # DietPi skips its own dietpi-wifi.txt import because AUTO_SETUP_NET_WIFI_ENABLED=0, so a
  # failed import on first boot means the key was never applied and must survive for a re-run.
  assert_not_contains "$DXB_CONSUMED_SECRETS" "WIFI_PASSWORD"
}

test_wifi_import_marks_secret_consumed_only_when_actually_imported() {
  net_env
  net_cfg 'PASSWORD=secretpass' 'WIFI_SSID=Home' 'WIFI_PASSWORD=wifipass1' 'WIFI_COUNTRY=US'
  DXB_MODE=run provision_network
  assert_contains "$DXB_CONSUMED_SECRETS" "WIFI_PASSWORD"
  net_env
  net_cfg 'PASSWORD=secretpass' 'WIFI_SSID=Home' 'WIFI_PASSWORD=wifipass1' 'WIFI_COUNTRY=US'
  printf '#!/bin/bash\nexit 1\n' > "$DXB_DIETPI_WIFIDB"; chmod +x "$DXB_DIETPI_WIFIDB"
  DXB_MODE=run provision_network 2> /dev/null
  assert_not_contains "$DXB_CONSUMED_SECRETS" "WIFI_PASSWORD"
  net_env
  net_cfg 'PASSWORD=secretpass' 'WIFI_SSID=Home' 'WIFI_PASSWORD=wifipass1' 'WIFI_COUNTRY=US'
  printf '#!/bin/bash\nexit 1\n' > "$DXB_DIETPI_WIFIDB"; chmod +x "$DXB_DIETPI_WIFIDB"
  DXB_MODE=first-boot provision_network 2> /dev/null
  assert_not_contains "$DXB_CONSUMED_SECRETS" "WIFI_PASSWORD"
  # The tool being absent altogether is the same story: nothing was applied.
  net_env
  net_cfg 'PASSWORD=secretpass' 'WIFI_SSID=Home' 'WIFI_PASSWORD=wifipass1' 'WIFI_COUNTRY=US'
  rm -f "$DXB_DIETPI_WIFIDB"
  DXB_MODE=first-boot provision_network 2> /dev/null
  assert_not_contains "$DXB_CONSUMED_SECRETS" "WIFI_PASSWORD"
  assert_contains "${DXB_FAILED_STEPS[*]}" "not found; WiFi credentials not imported"
}

test_provision_network_applied_wifi_password_skips_import() {
  net_env
  net_cfg 'PASSWORD=secretpass' 'WIFI_SSID=Home' 'WIFI_PASSWORD=<applied>' 'WIFI_COUNTRY=US'
  DXB_MODE=run provision_network
  assert_file_contains "$DXB_DIETPI_WIFI" "aWIFI_SSID[0]=''"
  assert_file_not_contains "$TEST_TMP/calls" "wifidb 1"
  assert_file_contains "$DXB_IFACES_DIR/eth0.conf" "iface eth0 inet dhcp"
  [[ -f $DXB_RESOLV_CONF ]] && _fail "resolv.conf must not be written in DHCP mode"
  # dxb_net_install_netwatch should fail gracefully if template is missing
  local saved_templates=$DXB_TEMPLATES
  rm -rf "$DXB_SYSTEMD_DIR"
  mkdir -p "$DXB_SYSTEMD_DIR"
  DXB_TEMPLATES=$TEST_TMP/empty-templates-2
  mkdir -p "$DXB_TEMPLATES"
  # shellcheck disable=SC2034
  DXB_FAILED_STEPS=()
  assert_fails dxb_net_install_netwatch 2> /dev/null
  assert_contains "${DXB_FAILED_STEPS[*]}" "dxberry-netwatch unit template"
  [[ -f $DXB_SYSTEMD_DIR/dxberry-netwatch.service ]] && _fail "netwatch service should not be created when template is missing"
  DXB_TEMPLATES=$saved_templates
}

# DietPi's WiFi-disabled path blacklists the WiFi modules, so wlan0 may simply not exist. The
# status file must never promise failover through an interface that is not there.
test_provision_network_enables_wifi_modules_and_checks_wlan0() {
  net_env
  net_cfg 'PASSWORD=secretpass' 'WIFI_SSID=Home' 'WIFI_PASSWORD=wifipass1' 'WIFI_COUNTRY=US'
  DXB_MODE=run provision_network
  assert_file_contains "$TEST_TMP/calls" "set_hw wifimodules enable"
  assert_contains "${DXB_STATUS_LINES[*]}" "wlan0 (Home) failover"
  assert_eq "${#DXB_FAILED_STEPS[@]}" "0"
  # wlan0 absent in run mode: a failed step, and no failover claim
  net_env
  rm -rf "$DXB_SYS_NET/wlan0"
  net_cfg 'PASSWORD=secretpass' 'WIFI_SSID=Home' 'WIFI_PASSWORD=wifipass1' 'WIFI_COUNTRY=US'
  DXB_MODE=run provision_network 2> /dev/null
  assert_contains "${DXB_FAILED_STEPS[*]}" "wlan0 not present under $DXB_SYS_NET"
  assert_not_contains "${DXB_STATUS_LINES[*]}" "failover"
  assert_contains "${DXB_STATUS_LINES[*]}" "eth0 only"
  # wlan0 absent on first boot: only a note, because the reboot still has to load the modules
  net_env
  rm -rf "$DXB_SYS_NET/wlan0"
  net_cfg 'PASSWORD=secretpass' 'WIFI_SSID=Home' 'WIFI_PASSWORD=wifipass1' 'WIFI_COUNTRY=US'
  DXB_MODE=first-boot provision_network 2> /dev/null
  assert_eq "${#DXB_FAILED_STEPS[@]}" "0"
  assert_contains "${DXB_STATUS_LINES[*]}" "wlan0: not present yet (WiFi modules enabled; check dxberry-netwatch --status after reboot)"
  assert_not_contains "${DXB_STATUS_LINES[*]}" "failover"
}

test_provision_network_reports_wifi_module_enable_failures() {
  net_env
  printf '#!/bin/bash\nexit 1\n' > "$DXB_DIETPI_SET_HW"; chmod +x "$DXB_DIETPI_SET_HW"
  net_cfg 'PASSWORD=secretpass' 'WIFI_SSID=Home' 'WIFI_PASSWORD=wifipass1' 'WIFI_COUNTRY=US'
  DXB_MODE=first-boot provision_network 2> /dev/null
  assert_contains "${DXB_FAILED_STEPS[*]}" "wifimodules enable failed"
  # A tool failure is reported once; it must not also produce the "not present yet" note, which
  # would claim the modules had been enabled.
  assert_not_contains "${DXB_STATUS_LINES[*]}" "not present yet"
  net_env
  rm -f "$DXB_DIETPI_SET_HW"
  net_cfg 'PASSWORD=secretpass' 'WIFI_SSID=Home' 'WIFI_PASSWORD=wifipass1' 'WIFI_COUNTRY=US'
  DXB_MODE=run provision_network 2> /dev/null
  assert_contains "${DXB_FAILED_STEPS[*]}" "cannot enable the WiFi modules"
}

# The single-configured-interface invariant only holds if nothing outside our own files names
# eth0 or wlan0. Foreign files are reported with file:line, never edited.
test_stray_stanza_scan_reports_file_and_line() {
  net_env
  net_cfg 'PASSWORD=secretpass'
  DXB_MODE=run provision_network
  # Our own eth0.conf carries "iface eth0 inet dhcp" and must be exempt.
  assert_ok dxb_net_scan_stray_stanzas
  assert_eq "${#DXB_FAILED_STEPS[@]}" "0"
  printf '# left behind by dietpi-config\nallow-hotplug eth0\niface eth0 inet dhcp\n' > "$DXB_IFACES_DIR/dietpi.conf"
  assert_fails dxb_net_scan_stray_stanzas 2> /dev/null
  assert_contains "${DXB_FAILED_STEPS[*]}" "stray stanza $DXB_IFACES_DIR/dietpi.conf:2: allow-hotplug eth0"
  assert_contains "${DXB_FAILED_STEPS[*]}" "stray stanza $DXB_IFACES_DIR/dietpi.conf:3: iface eth0 inet dhcp"
  assert_file_contains "$DXB_IFACES_DIR/dietpi.conf" "allow-hotplug eth0"
  # A second scan in the same run (the pre-reboot gate does one) still fails, but must not
  # list the same stanzas again.
  local before=${#DXB_FAILED_STEPS[@]}
  assert_fails dxb_net_scan_stray_stanzas 2> /dev/null
  assert_eq "${#DXB_FAILED_STEPS[@]}" "$before"
  # /etc/network/interfaces itself is scanned too, and provision_network reports through it.
  rm -f "$DXB_IFACES_DIR/dietpi.conf"
  # shellcheck disable=SC2034
  DXB_FAILED_STEPS=()
  # shellcheck disable=SC2034
  DXB_NET_STRAY_REPORTED=0
  printf 'source /etc/network/interfaces.d/*\nauto lo\niface lo inet loopback\n  auto wlan0\n' > "$DXB_INTERFACES_FILE"
  DXB_MODE=run provision_network 2> /dev/null
  assert_contains "${DXB_FAILED_STEPS[*]}" "stray stanza $DXB_INTERFACES_FILE:4:   auto wlan0"
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
  assert_contains "${DXB_FAILED_STEPS[*]}" "eth0 template"
  [[ -f $DXB_IFACES_DIR/eth0.conf ]] && _fail "eth0.conf should not be created when template is missing"
  DXB_TEMPLATES=$saved_templates
}
