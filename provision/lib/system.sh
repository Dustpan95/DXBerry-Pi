#!/bin/bash
# shellcheck shell=bash
# System settings: hostname, time zone, login password, serial console, SSH keys.

: "${DXB_HOSTNAME_FILE:=/etc/hostname}"
: "${DXB_HOSTS_FILE:=/etc/hosts}"
: "${DXB_LOCALTIME:=/etc/localtime}"
: "${DXB_TIMEZONE_FILE:=/etc/timezone}"
: "${DXB_ZONEINFO_DIR:=/usr/share/zoneinfo}"
: "${DXB_DIETPI_FUNC:=/boot/dietpi/func}"
: "${DXB_DIETPI_TXT:=/boot/dietpi.txt}"
: "${DXB_SSH_HOMES:=/root:root /home/dietpi:dietpi}"

dxb_sys_set_hostname() {
  local name=$1
  if [[ -x $DXB_DIETPI_FUNC/change_hostname ]]; then
    "$DXB_DIETPI_FUNC/change_hostname" "$name" > /dev/null 2>&1 && return 0
  fi
  printf '%s\n' "$name" > "$DXB_HOSTNAME_FILE"
  if grep -q '^127\.0\.1\.1[[:blank:]]' "$DXB_HOSTS_FILE" 2> /dev/null; then
    n="$name" awk '{ if ($1 == "127.0.1.1") print "127.0.1.1 " ENVIRON["n"]; else print }' "$DXB_HOSTS_FILE" > "$DXB_HOSTS_FILE.dxbtmp" && mv "$DXB_HOSTS_FILE.dxbtmp" "$DXB_HOSTS_FILE"
  else
    printf '127.0.1.1 %s\n' "$name" >> "$DXB_HOSTS_FILE"
  fi
  hostname "$name" 2> /dev/null || true
}

# dxb_sys_add_authorized_key HOME KEY [OWNER]: idempotent append with safe permissions.
dxb_sys_add_authorized_key() {
  local home=$1 key=$2 owner=${3:-root} f="$1/.ssh/authorized_keys"
  [[ -d $home ]] || return 0
  mkdir -p "$home/.ssh"; chmod 700 "$home/.ssh"
  [[ -f $f ]] || : > "$f"
  chmod 600 "$f"
  grep -qxF -- "$key" "$f" || printf '%s\n' "$key" >> "$f"
  chown -R "$owner:" "$home/.ssh" 2> /dev/null || true
}

dxb_sys_serial_console() {
  local want cur
  [[ ${DXB_CFG[SERIAL_CONSOLE]} == on ]] && want=1 || want=0
  cur=$(sed -n 's/^[[:blank:]]*CONFIG_SERIAL_CONSOLE_ENABLE=//p' "$DXB_DIETPI_TXT" 2> /dev/null | head -1)
  [[ $cur == "$want" ]] && return 0
  if [[ -x $DXB_DIETPI_FUNC/dietpi-set_hardware ]]; then
    if (( want )); then "$DXB_DIETPI_FUNC/dietpi-set_hardware" serialconsole enable > /dev/null 2>&1
    else "$DXB_DIETPI_FUNC/dietpi-set_hardware" serialconsole disable > /dev/null 2>&1; fi
  fi
  dxb_set_kv "$DXB_DIETPI_TXT" CONFIG_SERIAL_CONSOLE_ENABLE "$want"
  dxb_info "serial console $( (( want )) && echo enabled || echo disabled )"
}

provision_system() {
  local tz=${DXB_CFG[TIMEZONE]} entry home owner
  if [[ $(cat "$DXB_HOSTNAME_FILE" 2> /dev/null) != "${DXB_CFG[HOSTNAME]}" ]]; then
    dxb_sys_set_hostname "${DXB_CFG[HOSTNAME]}"
    dxb_info "hostname set to ${DXB_CFG[HOSTNAME]}"
  fi
  if [[ -f $DXB_ZONEINFO_DIR/$tz ]]; then
    if [[ $(readlink "$DXB_LOCALTIME" 2> /dev/null) != "$DXB_ZONEINFO_DIR/$tz" ]]; then
      ln -sfn "$DXB_ZONEINFO_DIR/$tz" "$DXB_LOCALTIME"
      printf '%s\n' "$tz" > "$DXB_TIMEZONE_FILE"
      dxb_info "time zone set to $tz"
    fi
  else
    dxb_step_failed system "time zone $tz not found under $DXB_ZONEINFO_DIR"
  fi
  if [[ ${DXB_CFG[PASSWORD]} != "$DXB_APPLIED" && ${DXB_MODE:-run} != first-boot ]]; then
    if printf 'root:%s\n' "${DXB_CFG[PASSWORD]}" | chpasswd && printf 'dietpi:%s\n' "${DXB_CFG[PASSWORD]}" | chpasswd; then
      dxb_info "login password updated for root and dietpi"
      dxb_secret_consumed PASSWORD
    else
      dxb_step_failed system "chpasswd failed to update the login password"
    fi
  fi
  [[ ${DXB_MODE:-run} == first-boot ]] || dxb_sys_serial_console
  if [[ -n ${DXB_CFG[SSH_PUBKEY]:-} ]]; then
    for entry in $DXB_SSH_HOMES; do
      home=${entry%%:*}; owner=${entry#*:}
      dxb_sys_add_authorized_key "$home" "${DXB_CFG[SSH_PUBKEY]}" "$owner"
    done
    dxb_info "SSH public key installed"
  fi
  dxb_status_add "hostname: ${DXB_CFG[HOSTNAME]}"
  dxb_status_add "time zone: $tz"
  return 0
}
