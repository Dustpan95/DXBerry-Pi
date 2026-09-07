#!/bin/bash
# DXBerry-Pi: runs once DietPi's first-run installs are done. See /opt/dxberry.
[[ -x /opt/dxberry/bin/dxberry-provision ]] || exit 0
exec /opt/dxberry/bin/dxberry-provision --first-boot
