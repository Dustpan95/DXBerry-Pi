#!/bin/bash
# shellcheck shell=bash
# The web console: Cockpit from Debian plus the DXBerry page (console spec sections 5, 6.1 and 12).

: "${DXB_COCKPIT_PACKAGES:=cockpit-ws cockpit-bridge cockpit-system}"
: "${DXB_COCKPIT_SRC:=/opt/dxberry/cockpit/dxberry}"
: "${DXB_COCKPIT_LINK:=/usr/share/cockpit/dxberry}"
: "${DXB_COCKPIT_DROPIN:=/etc/systemd/system/cockpit.socket.d/dxberry-listen.conf}"
: "${DXB_TEMPLATES:=/opt/dxberry/templates}"

# dxb_console_installed: 0 when every Cockpit package is installed (not merely unpacked or removed).
dxb_console_installed() {
  local p
  for p in $DXB_COCKPIT_PACKAGES; do
    [[ $(dpkg-query -W -f '${db:Status-Status}' "$p" 2> /dev/null) == installed ]] || return 1
  done
}

# The packages' recommends pull NetworkManager and PackageKit, which would fight dxberry-netwatch
# and ifupdown (measured on Debian 13), so --no-install-recommends is not optional here.
dxb_console_install() {
  dxb_console_installed && return 0
  # shellcheck disable=SC2086
  if DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends $DXB_COCKPIT_PACKAGES > /dev/null 2>&1 && dxb_console_installed; then
    dxb_info "installed Cockpit ($DXB_COCKPIT_PACKAGES)"
    return 0
  fi
  dxb_step_failed console "could not install $DXB_COCKPIT_PACKAGES (no network?); the console is unavailable until a re-run"
  return 1
}

# DietPi masks systemd-logind; without it Cockpit's own Restart and Shut down fail and every login
# logs a failed session. Re-asserted on every run, since a DietPi update could mask it again.
dxb_console_logind() {
  if [[ $(systemctl is-enabled systemd-logind 2> /dev/null) == masked ]]; then
    systemctl unmask systemd-logind > /dev/null 2>&1 || { dxb_step_failed console "could not unmask systemd-logind"; return 1; }
    dxb_info "systemd-logind unmasked"
  fi
  systemctl start systemd-logind > /dev/null 2>&1 || { dxb_step_failed console "could not start systemd-logind"; return 1; }
}

# dxb_console_listen: the 443 + 80 drop-in. 0 written, 1 unchanged, 2 failed (step recorded).
dxb_console_listen() {
  local content
  content=$(< "$DXB_TEMPLATES/cockpit-listen.conf") || { dxb_step_failed console "template cockpit-listen.conf is missing"; return 2; }
  mkdir -p "$(dirname "$DXB_COCKPIT_DROPIN")" 2> /dev/null
  dxb_write_if_changed "$DXB_COCKPIT_DROPIN" "$content" 644 || return 1
  [[ -f $DXB_COCKPIT_DROPIN && $(< "$DXB_COCKPIT_DROPIN") == "$content" ]] && return 0
  dxb_step_failed console "could not write $DXB_COCKPIT_DROPIN"
  return 2
}

# dxb_console_link: Cockpit's package directory entry for the DXBerry page. A new session finds
# it; nothing needs restarting. 0 in place, 1 failed (step recorded).
dxb_console_link() {
  [[ -d $DXB_COCKPIT_SRC ]] || { dxb_step_failed console "the DXBerry page is missing ($DXB_COCKPIT_SRC)"; return 1; }
  [[ -L $DXB_COCKPIT_LINK && $(readlink "$DXB_COCKPIT_LINK") == "$DXB_COCKPIT_SRC" ]] && return 0
  if [[ -e $DXB_COCKPIT_LINK && ! -L $DXB_COCKPIT_LINK ]]; then
    dxb_step_failed console "$DXB_COCKPIT_LINK exists and is not DXBerry's link; left alone"
    return 1
  fi
  mkdir -p "$(dirname "$DXB_COCKPIT_LINK")" 2> /dev/null
  if ! ln -sfn "$DXB_COCKPIT_SRC" "$DXB_COCKPIT_LINK" || [[ $(readlink "$DXB_COCKPIT_LINK") != "$DXB_COCKPIT_SRC" ]]; then
    dxb_step_failed console "could not link $DXB_COCKPIT_LINK"
    return 1
  fi
  dxb_info "DXBerry page linked into Cockpit"
}

# dxb_console_listening_443: cockpit.socket's own current bind addresses include :443 - the
# drop-in has actually taken effect. systemctl show -p Listen prints one "Listen=..." line per
# bound address, e.g. "Listen=[::]:443 (Stream)".
dxb_console_listening_443() {
  systemctl show -p Listen cockpit.socket 2> /dev/null | grep -qE ':443([^0-9]|$)'
}

# provision_console (console spec section 12): runs after the radio step. Never fatal: every
# problem is a failed step named console and the rest of the run goes on.
provision_console() {
  local rc restart_failed=0
  if [[ ${DXB_CFG[CONSOLE]:-on} == off ]]; then
    if systemctl cat cockpit.socket > /dev/null 2>&1; then
      systemctl disable --now cockpit.socket > /dev/null 2>&1 || dxb_step_failed console "could not stop cockpit.socket"
    fi
    if [[ -L $DXB_COCKPIT_LINK ]]; then rm -f "$DXB_COCKPIT_LINK" || dxb_step_failed console "could not remove $DXB_COCKPIT_LINK"; fi
    dxb_status_add "console: off (CONSOLE=off)"
    return 0
  fi
  if ! dxb_console_install; then
    dxb_status_add "console: unavailable (Cockpit is not installed)"
    return 0
  fi
  dxb_console_logind
  dxb_console_listen; rc=$?
  dxb_console_link
  # the package's own unit starts on 9090 when installed; the drop-in moves it to 443 and 80.
  # Also restart when the drop-in is unchanged but the socket is not actually listening on 443
  # (an earlier restart here failed, for instance): otherwise that state never heals before a
  # reboot, since an unchanged drop-in on its own is not proof the restart it needed ever ran.
  if (( rc == 0 )) || { (( rc == 1 )) && ! dxb_console_listening_443; }; then
    if systemctl daemon-reload > /dev/null 2>&1 && systemctl restart cockpit.socket > /dev/null 2>&1; then
      dxb_info "console listening on ports 443 and 80"
    else
      dxb_step_failed console "could not restart cockpit.socket on ports 443 and 80"
      restart_failed=1
    fi
  fi
  systemctl enable --now cockpit.socket > /dev/null 2>&1 || dxb_step_failed console "could not enable cockpit.socket"
  if (( rc == 2 )) || (( restart_failed )); then
    dxb_status_add "console: unavailable (see FAILED STEPS)"
  else
    dxb_status_add "console: https://${DXB_CFG[_IP]:-<this-pi>}/ (log in as dietpi)"
  fi
  return 0
}
