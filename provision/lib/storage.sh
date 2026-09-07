#!/bin/bash
# shellcheck shell=bash
# Storage write-minimization (tier 1): journald in RAM, zram swap check.

: "${DXB_JOURNALD_DROPIN:=/etc/systemd/journald.conf.d/dxberry.conf}"
: "${DXB_TEMPLATES:=/opt/dxberry/templates}"

provision_storage() {
  local content
  mkdir -p "$(dirname "$DXB_JOURNALD_DROPIN")"
  if ! content=$(dxb_render "$DXB_TEMPLATES/journald-dxberry.conf"); then
    dxb_step_failed storage "journald template missing"
    return 1
  fi
  if dxb_write_if_changed "$DXB_JOURNALD_DROPIN" "$content"; then
    systemctl restart systemd-journald 2> /dev/null || true
    dxb_info "journald set to volatile storage"
  fi
  if swapon --show=NAME --noheadings 2> /dev/null | grep -q zram; then
    dxb_status_add "swap: zram"
  else
    dxb_warn "swap is not on zram; DietPi applies AUTO_SETUP_SWAPFILE_LOCATION=zram at first boot, or change it in dietpi-config"
    dxb_status_add "swap: not zram (see provision.log)"
  fi
  dxb_status_add "logs: RAM only (journald volatile, DietPi RAMlog)"
  return 0
}
