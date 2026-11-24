#!/bin/bash
# shellcheck shell=bash
# Native collection hooks run as root inside the existing systemd barriers.
# The daemon owns routing/approval/recovery. This wrapper never edits Qt files.
: "${DXB_STATION_CMD:=/opt/dxberry/bin/dxberryd}"
: "${DXB_PROGRAM_RUN:=/run/dxberry/station-programs}"

dxb_wsjtx_hook() {
  local action=$1 session=$2 radio
  [[ $session == wsjtx-* ]] || return 0
  radio=${session#wsjtx-}
  [[ $radio =~ ^[a-z][a-z0-9]{0,11}$ ]] || return 2
  "$DXB_STATION_CMD" -state "$DXB_STATE_DIR/station" -program-run "$DXB_PROGRAM_RUN" \
    -wsjtx-hook "$action" -wsjtx-radio "$radio" -wsjtx-invocation "${INVOCATION_ID:-}" \
    || { dxb_error "$session: native program routing could not be verified; see the station service"; return 7; }
}
