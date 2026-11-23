#!/bin/bash
set -euo pipefail
log=/evidence/systemd-guest.log
set +e
timeout --signal=TERM --kill-after=5 420 qemu-system-x86_64 \
  -accel tcg,thread=multi -machine q35 -cpu max -smp 2 -m 2048 \
  -nodefaults -nic none -display none -serial stdio -monitor none -no-reboot \
  -kernel /vm/vmlinuz -initrd /vm/root.cpio.gz \
  -append 'console=ttyS0 rdinit=/init panic=-1 loglevel=4 systemd.show_status=no' \
  2>&1 | tee "$log"
result=${PIPESTATUS[0]}
set -e
(( result == 0 ))
[[ $(grep -c '^DXB_MANAGER_RESULT=PASS' "$log") == 1 ]]
if grep -q '^DXB_MANAGER_RESULT=FAIL' "$log"; then exit 1; fi
echo 'Real guest systemd queue checks passed; hardware and retirement acceptance remain separate.'
