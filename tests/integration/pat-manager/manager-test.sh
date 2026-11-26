#!/bin/bash
set -uo pipefail
export DXB_PAT_MANAGER_FIXTURE=1
if /fixture/pat-manager.test -test.v -test.timeout=175s -test.run '^TestPat(RealManagerCompatibility|ProductionSession)$'; then
  echo 'DXB_MANAGER_RESULT=PASS'
else
  journalctl --no-pager -n 100 -u dxberry-screen@pat-fixture1.service -u dxberry-screen@pat-fixture2.service
  echo 'DXB_MANAGER_RESULT=FAIL'
fi
systemctl poweroff --force --force
