#!/bin/bash
set -euo pipefail
[[ $# == 1 && ( $1 == once || $1 == hold ) ]]
export DXB_STATE_DIR=/var/lib/dxb-native-state DXB_RUN_DIR=/run/dxb-console
export DXB_PROGRAM_RUN=/run/dxb-native-hooks DXB_OPT=/opt/dxberry
# shellcheck source=provision/lib/common.sh
source "$DXB_OPT/lib/common.sh"
# shellcheck source=provision/lib/update.sh
source "$DXB_OPT/lib/update.sh"
_dxb_update_program_release_guard /run/dxb-incompatible
if [[ $1 == hold ]]; then
  printf 'DXB_RELEASE_GUARD_READY\n'
  IFS= read -r reply
  [[ $reply == finish ]]
fi
