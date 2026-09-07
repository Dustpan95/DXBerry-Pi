#!/bin/bash
# DXBerry-Pi: runs on first boot before the network is up. See /opt/dxberry.
[[ -x /opt/dxberry/bin/dxberry-preboot ]] || exit 0
exec /opt/dxberry/bin/dxberry-preboot
