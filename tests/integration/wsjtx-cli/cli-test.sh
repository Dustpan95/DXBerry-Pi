#!/bin/bash
set -uo pipefail
export DXB_WSJTX_MANAGER_FIXTURE=1
if /fixture/wsjtx-manager.test -test.v -test.timeout=180s -test.run '^TestWSJTXPublicCLICompatibility$'; then
  echo 'DXB_MANAGER_RESULT=PASS'
else
  journalctl --no-pager -n 100 -u dxberry-screen@wsjtx-fixture1.service -u dxb-wsjtx-pulse.service
  echo 'DXB_MANAGER_RESULT=FAIL'
fi
systemctl poweroff --force --force
