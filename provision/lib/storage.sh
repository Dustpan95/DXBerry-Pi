#!/bin/bash
# shellcheck shell=bash
# Storage write-minimization (tier 1): a small capped journal on the drive, zram swap check.

: "${DXB_JOURNALD_DROPIN:=/etc/systemd/journald.conf.d/dxberry.conf}"
: "${DXB_JOURNAL_DIR:=/var/lib/dxberry/journal}"
: "${DXB_SYSTEMD_DIR:=/etc/systemd/system}"
: "${DXB_TEMPLATES:=/opt/dxberry/templates}"

provision_storage() {
  local content link
  mkdir -p "$(dirname "$DXB_JOURNALD_DROPIN")" "$DXB_SYSTEMD_DIR"
  if ! content=$(dxb_render "$DXB_TEMPLATES/journald-dxberry.conf") ||
    ! link=$(dxb_render "$DXB_TEMPLATES/dxberry-journal-link.service"); then
    dxb_step_failed storage "journald template missing"
    return 1
  fi
  mkdir -p "$DXB_JOURNAL_DIR"
  # the link first: without it journald would make its directory in the RAM /var/log, where
  # DietPi's hourly cleanup empties the files
  if dxb_write_if_changed "$DXB_SYSTEMD_DIR/dxberry-journal-link.service" "$link"; then
    systemctl daemon-reload 2> /dev/null || true
    if ! systemctl enable --now dxberry-journal-link.service 2> /dev/null; then
      dxb_step_failed storage "could not link the journal to the drive; logs stay in RAM"
      return 1
    fi
  fi
  if dxb_write_if_changed "$DXB_JOURNALD_DROPIN" "$content"; then
    systemctl restart systemd-journald 2> /dev/null || true
    journalctl --flush 2> /dev/null || true
    dxb_info "journald set to keep up to 64 MB on the drive"
  fi
  if swapon --show=NAME --noheadings 2> /dev/null | grep -q zram; then
    dxb_status_add "swap: zram"
  else
    dxb_warn "swap is not on zram; DietPi applies AUTO_SETUP_SWAPFILE_LOCATION=zram at first boot, or change it in dietpi-config"
    dxb_status_add "swap: not zram (see provision.log)"
  fi
  dxb_status_add "logs: journal on the drive (up to 64 MB), the rest in RAM (DietPi RAMlog)"
  return 0
}
