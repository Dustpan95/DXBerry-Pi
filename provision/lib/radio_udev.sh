#!/bin/bash
# shellcheck shell=bash
# Derived udev rules and ALSA slot pinning for pinned radios (spec sections 7.1, 7.2).

: "${DXB_TEMPLATES:=/opt/dxberry/templates}"
: "${DXB_UDEV_RULES_FILE:=/etc/udev/rules.d/70-dxberry-radio.rules}"
: "${DXB_MODPROBE_FILE:=/etc/modprobe.d/dxberry-audio.conf}"
: "${DXB_UDEVADM:=udevadm}"

dxb_radio_udev_rules() {
  cat "$DXB_TEMPLATES/70-dxberry-radio.rules.head"
  jq -r '.radios | to_entries[] | .key as $n | .value as $r
    | "", "# \($n) - \($r.label // "")",
      (if $r.audio then "SUBSYSTEM==\"sound\", KERNEL==\"card*\", ENV{ID_PATH}==\"*-\($r.audio.path)\", ATTR{id}=\"\($n | ascii_upcase)\", TAG+=\"dxberry-radio\"" else empty end),
      (if $r.cat then "SUBSYSTEM==\"tty\", ENV{ID_PATH}==\"*-\($r.cat.path)\", SYMLINK+=\"dxberry/\($n)-cat\", TAG+=\"dxberry-radio\"" else empty end),
      (if $r.ptt_serial then "SUBSYSTEM==\"tty\", ENV{ID_PATH}==\"*-\($r.ptt_serial.path)\", SYMLINK+=\"dxberry/\($n)-ptt\", TAG+=\"dxberry-radio\"" else empty end),
      (if $r.hid then "SUBSYSTEM==\"hidraw\", ENV{ID_PATH}==\"*-\($r.hid.path)\", SYMLINK+=\"dxberry/\($n)-hid\", TAG+=\"dxberry-radio\"" else empty end)' <<< "$1"
  printf '\nTAG=="dxberry-radio", ACTION=="add|remove", RUN+="/bin/systemctl --no-block start dxberry-radio-hotplug.service"\n'
}

# 0 written (udev reloaded and re-triggered), 1 unchanged, 6 could not write.
dxb_radio_udev_write() {
  local content
  content=$(dxb_radio_udev_rules "$1") || return 6
  mkdir -p "$(dirname "$DXB_UDEV_RULES_FILE")" 2> /dev/null
  if dxb_write_if_changed "$DXB_UDEV_RULES_FILE" "$content" 644; then
    [[ $(< "$DXB_UDEV_RULES_FILE") == "$content" ]] || { dxb_error "could not write $DXB_UDEV_RULES_FILE"; return 6; }
    dxb_info "udev rules written to $DXB_UDEV_RULES_FILE"
    "$DXB_UDEVADM" control --reload || dxb_warn "udevadm control --reload failed"
    "$DXB_UDEVADM" trigger --action=add --subsystem-match=sound --subsystem-match=tty --subsystem-match=hidraw || dxb_warn "udevadm trigger failed"
    return 0
  fi
  return 1
}

dxb_radio_modprobe_install() {
  local content
  content=$(< "$DXB_TEMPLATES/dxberry-audio.conf") || return 6
  mkdir -p "$(dirname "$DXB_MODPROBE_FILE")" 2> /dev/null
  if dxb_write_if_changed "$DXB_MODPROBE_FILE" "$content" 644; then
    [[ $(< "$DXB_MODPROBE_FILE") == "$content" ]] || return 6
    dxb_info "ALSA slot pinning installed ($DXB_MODPROBE_FILE; takes effect at next boot)"
    return 0
  fi
  return 1
}
