#!/bin/bash
# Each QEMU process gets a fresh kernel/initramfs. Only this disk survives.
set -euo pipefail
[[ ! -e /tmp/update-recovery.img && $(stat -f -c %T /tmp) == tmpfs ]]
truncate -s 192M /tmp/update-recovery.img
DXB_RECOVERY_PHASE=prepare /run-test
DXB_RECOVERY_PHASE=verify /run-test
