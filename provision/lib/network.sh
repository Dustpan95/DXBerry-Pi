#!/bin/bash
# shellcheck shell=bash
# Network provisioning: interfaces files, resolv.conf, WiFi credentials, dxberry-netwatch.

: "${DXB_IFACES_DIR:=/etc/network/interfaces.d}"
: "${DXB_INTERFACES_FILE:=/etc/network/interfaces}"
: "${DXB_RESOLV_CONF:=/etc/resolv.conf}"
: "${DXB_TEMPLATES:=/opt/dxberry/templates}"
: "${DXB_SYSTEMD_DIR:=/etc/systemd/system}"
: "${DXB_DIETPI_WIFIDB:=/boot/dietpi/func/dietpi-wifidb}"
: "${DXB_DIETPI_SET_HW:=/boot/dietpi/func/dietpi-set_hardware}"
: "${DXB_DIETPI_WIFI:=/boot/dietpi-wifi.txt}"
: "${DXB_SYS_NET:=/sys/class/net}"
# shellcheck disable=SC2034
DXB_NET_CHANGED=0
# Set once dxb_net_scan_stray_stanzas has recorded its findings, so the second call in a run
# (the pre-reboot gate) re-reads the files without listing the same stanzas twice.
DXB_NET_STRAY_REPORTED=0

# Shared between dxb_net_scan_stray_stanzas and dxb_net_clean_main_interfaces so the two can
# never drift apart: if one is widened without the other, the cleaner would stop removing a
# stanza the scan still flags, and the first-boot gate would block forever. Guarded so that
# re-sourcing this file (several test files and dxberry-preboot all source it) does not hit
# "readonly variable".
if [[ -z ${DXB_NET_ETH_WLAN_AUTO_RE:-} ]]; then
  readonly DXB_NET_ETH_WLAN_AUTO_RE='^[[:blank:]]*(auto|allow-hotplug)[[:blank:]].*\b(eth0|wlan0)\b'
  readonly DXB_NET_ETH_WLAN_IFACE_RE='^[[:blank:]]*iface[[:blank:]]+(eth0|wlan0)\b'
fi

# dxb_net_write_wifi_txt FILE SSID KEY: fill slot 0 of a DietPi dietpi-wifi.txt. Holds a WiFi
# PSK, so it must never be left world/group-readable, including if the import below fails.
dxb_net_write_wifi_txt() {
  local file=$1 ssid=$2 key=$3
  dxb_set_kv "$file" 'aWIFI_SSID[0]' "$(dxb_squote "$ssid")"
  dxb_set_kv "$file" 'aWIFI_KEY[0]' "$(dxb_squote "$key")"
  dxb_set_kv "$file" 'aWIFI_KEYMGR[0]' "'WPA-PSK'"
  chmod 600 "$file" 2> /dev/null || true
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
# Runs in every mode: dxberry-preboot and boot/dietpi.overrides.txt both set
# AUTO_SETUP_NET_WIFI_ENABLED=0, and with WiFi disabled DietPi's automated first run never
# imports /boot/dietpi-wifi.txt itself - so this import is the only one that ever happens, and
# WIFI_PASSWORD counts as consumed only when it succeeds.
dxb_net_import_wifi() {
  [[ ${DXB_CFG[WIFI_PASSWORD]} == "$DXB_APPLIED" ]] && return 0
  dxb_net_write_wifi_txt "$DXB_DIETPI_WIFI" "${DXB_CFG[WIFI_SSID]}" "${DXB_CFG[WIFI_PASSWORD]}"
  if [[ ! -x $DXB_DIETPI_WIFIDB ]]; then
    dxb_step_failed network "$DXB_DIETPI_WIFIDB not found; WiFi credentials not imported"
    return 1
  fi
  if ! "$DXB_DIETPI_WIFIDB" 1 > /dev/null 2>&1; then
    dxb_step_failed network "dietpi-wifidb failed to import WiFi credentials"
    return 1
  fi
  dxb_info "WiFi credentials imported for ${DXB_CFG[WIFI_SSID]}"
  dxb_secret_consumed WIFI_PASSWORD
}

# DietPi's WiFi-disabled path blacklists the WiFi kernel modules, so wlan0 need not exist even
# when the adapter does. Undo that, unblock rfkill, and report whether wlan0 actually appeared.
# 0 = wlan0 present, 1 = wlan0 absent, 2 = the modules could not be enabled (already reported).
dxb_net_enable_wifi_hw() {
  if [[ ! -x $DXB_DIETPI_SET_HW ]]; then
    dxb_step_failed network "$DXB_DIETPI_SET_HW not found; cannot enable the WiFi modules"
    return 2
  fi
  if ! "$DXB_DIETPI_SET_HW" wifimodules enable > /dev/null 2>&1; then
    dxb_step_failed network "dietpi-set_hardware wifimodules enable failed"
    return 2
  fi
  rfkill unblock wifi 2> /dev/null || true
  [[ -d $DXB_SYS_NET/wlan0 ]]
}

# The single-configured-interface invariant assumes ifupdown learns about eth0/wlan0 only from
# our own interfaces.d/<iface>.conf files. Report every foreign stanza that would break it - a
# duplicate iface, or an auto/allow-hotplug line that lets something else raise the interface.
# Foreign files are never edited. Returns non-zero when anything was found.
dxb_net_scan_stray_stanzas() {
  local f files=() hit line loc num text hits=0
  if [[ -f $DXB_INTERFACES_FILE ]]; then files+=("$DXB_INTERFACES_FILE"); fi
  for f in "$DXB_IFACES_DIR"/*; do
    [[ -f $f ]] || continue
    case ${f##*/} in
      eth0.conf|wlan0.conf) continue ;;
    esac
    files+=("$f")
  done
  (( ${#files[@]} )) || return 0
  while IFS= read -r hit; do
    [[ -n $hit ]] || continue
    loc=${hit%%:*}; line=${hit#*:}; num=${line%%:*}; text=${line#*:}
    if (( ! DXB_NET_STRAY_REPORTED )); then dxb_step_failed network "stray stanza $loc:$num: $text"; fi
    hits=1
  done < <(grep -nHE "${DXB_NET_ETH_WLAN_AUTO_RE}|${DXB_NET_ETH_WLAN_IFACE_RE}" "${files[@]}" 2> /dev/null)
  if (( hits )); then DXB_NET_STRAY_REPORTED=1; fi
  (( hits == 0 ))
}

# DietPi's automated first run leaves its own eth0/wlan0 auto/allow-hotplug lines and iface
# stanzas in the MAIN /etc/network/interfaces file, alongside the "source interfaces.d/*"
# drop-in include - hardware proved this, contrary to the original assumption that the main
# file only sourced interfaces.d/*. Unlike files under interfaces.d/, the main file is DietPi's
# own generated file, not a user's, so the provisioner owns cleaning it before the stray scan.
# 0 = success (changed or not, including "file does not exist"). 1 = the rewrite failed
# (reported via dxb_step_failed).
dxb_net_clean_main_interfaces() {
  local file=$DXB_INTERFACES_FILE
  [[ -f $file ]] || return 0
  local -a out=()
  local line inblock=0 has_source=0
  local direct_re=$DXB_NET_ETH_WLAN_AUTO_RE
  local block_start_re=$DXB_NET_ETH_WLAN_IFACE_RE
  local term_re='^[[:blank:]]*(iface|auto|allow-[A-Za-z0-9_-]+|source-directory|source|mapping|rename)([[:blank:]]|$)'
  # Debian/DietPi's stock file uses the absolute form; an optional /etc/network/ prefix on
  # either directive still counts as "our drop-ins are already sourced".
  local source_re='^[[:blank:]]*(source[[:blank:]]+(/etc/network/)?interfaces\.d/\*|source-directory[[:blank:]]+(/etc/network/)?interfaces\.d)[[:blank:]]*$'
  while IFS= read -r line || [[ -n $line ]]; do
    if (( inblock )) && [[ $line =~ $term_re ]]; then inblock=0; fi
    if (( inblock )); then continue; fi
    if [[ $line =~ $direct_re ]]; then continue; fi
    if [[ $line =~ $block_start_re ]]; then inblock=1; continue; fi
    out+=("$line")
    [[ $line =~ $source_re ]] && has_source=1
  done < "$file"
  (( has_source )) || out+=('source interfaces.d/*')
  local newcontent=''
  (( ${#out[@]} )) && newcontent=$(printf '%s\n' "${out[@]}")
  if dxb_write_if_changed "$file" "$newcontent"; then
    if [[ $(< "$file") == "$newcontent" ]]; then
      DXB_NET_CHANGED=1
      dxb_info "removed DietPi eth0/wlan0 stanzas from $file"
    else
      dxb_step_failed network "could not rewrite $file"
      return 1
    fi
  fi
  return 0
}

dxb_net_install_netwatch() {
  local unit="$DXB_SYSTEMD_DIR/dxberry-netwatch.service" content rc=0
  if ! content=$(dxb_render "$DXB_TEMPLATES/dxberry-netwatch.service"); then
    dxb_step_failed network "dxberry-netwatch unit template missing"
    return 1
  fi
  if dxb_write_if_changed "$unit" "$content"; then
    dxb_info "dxberry-netwatch unit installed"
    if ! systemctl daemon-reload; then
      dxb_step_failed network "systemctl daemon-reload failed after writing $unit"
      rc=1
    fi
  fi
  # Unconditional and checked: enable is idempotent, and a unit that is byte-identical but not
  # enabled (a hand-run "systemctl disable", a half-finished earlier run) must still be fixed.
  if ! systemctl enable dxberry-netwatch > /dev/null 2>&1; then
    dxb_step_failed network "systemctl enable dxberry-netwatch failed"
    rc=1
  fi
  return $rc
}

provision_network() {
  local content wlan_ok=0 wlan_note=0 hw_rc
  mkdir -p "$DXB_IFACES_DIR"
  content=$(dxb_net_render_iface eth0) || { dxb_step_failed network "eth0 template failed"; return 1; }
  dxb_write_if_changed "$DXB_IFACES_DIR/eth0.conf" "$content" && { DXB_NET_CHANGED=1; dxb_info "wrote eth0.conf (${DXB_CFG[_MODE]})"; }
  if (( DXB_CFG[_WIFI] )); then
    content=$(dxb_net_render_iface wlan0) || { dxb_step_failed network "wlan0 template failed"; return 1; }
    dxb_write_if_changed "$DXB_IFACES_DIR/wlan0.conf" "$content" && { DXB_NET_CHANGED=1; dxb_info "wrote wlan0.conf"; }
    dxb_net_enable_wifi_hw
    hw_rc=$?
    if (( hw_rc == 0 )); then
      wlan_ok=1
    elif (( hw_rc == 1 )); then
      if [[ ${DXB_MODE:-run} == first-boot ]]; then
        # The modules were just unblacklisted; the reboot at the end of first boot loads them.
        dxb_warn "wlan0 is not present yet; the WiFi modules were only just enabled"
        wlan_note=1
      else
        dxb_step_failed network "wlan0 not present under $DXB_SYS_NET after enabling the WiFi modules; check the adapter, then reboot and re-run"
      fi
    fi
    dxb_net_import_wifi
  elif [[ -f $DXB_IFACES_DIR/wlan0.conf ]]; then
    rm -f "$DXB_IFACES_DIR/wlan0.conf"
    # shellcheck disable=SC2034
    DXB_NET_CHANGED=1
    dxb_info "removed wlan0.conf (no WIFI_SSID)"
  fi
  # Our own files are in place; anything else naming eth0/wlan0 is a failed step, never an edit.
  # DietPi's own eth0/wlan0 stanzas in the main file are stripped first - the scan still runs
  # even if that failed, which reports them and blocks the gate: the safe outcome.
  dxb_net_clean_main_interfaces
  dxb_net_scan_stray_stanzas
  if [[ ${DXB_CFG[_MODE]} == static ]]; then
    # shellcheck disable=SC2086
    content=$(printf 'nameserver %s\n' ${DXB_CFG[DNS]})
    dxb_write_if_changed "$DXB_RESOLV_CONF" "$content" && dxb_info "wrote resolv.conf"
  fi
  dxb_net_install_netwatch
  if (( wlan_ok )); then dxb_status_add "network: ${DXB_CFG[_MODE]} ${DXB_CFG[STATIC_IP]:-} - eth0 primary, wlan0 (${DXB_CFG[WIFI_SSID]}) failover"
  else dxb_status_add "network: ${DXB_CFG[_MODE]} ${DXB_CFG[STATIC_IP]:-} - eth0 only"; fi
  if (( wlan_note )); then dxb_status_add "wlan0: not present yet (WiFi modules enabled; check dxberry-netwatch --status after reboot)"; fi
  return 0
}
