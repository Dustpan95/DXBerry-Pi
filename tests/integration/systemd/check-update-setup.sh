#!/bin/bash
# The updater invokes this hook inside its transient guest worker after a swap.
set -euo pipefail
[[ $(hostname) == dxb-ci-guest && -f /fixture/packages/dxberry-pi-0.0.2.tar.gz ]]
[[ -n ${INVOCATION_ID:-} && $DXB_LIB == /opt/dxberry/lib && $DXB_GW_UPGRADE == 0 ]]
grep -Fq "/$DXB_UPDATE_RUN_UNIT.service" /proc/self/cgroup
/opt/dxberry/bin/dxberry-provision --check > /dev/null
systemctl restart dxberryd.service
printf '%s %s\n' "$DXB_UPDATE_RUN_UNIT" "$(< /opt/dxberry/VERSION)" >> /run/dxb-ci/setup-history
