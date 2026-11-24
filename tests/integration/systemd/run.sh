#!/bin/bash
set -euo pipefail
log=/evidence/systemd-guest.log
set +e
timeout --signal=TERM --kill-after=5 420 qemu-system-x86_64 \
  -accel tcg,thread=multi -machine q35,usb=off -cpu max -smp 2 -m 2048 \
  -nodefaults -nic none -display none -serial stdio -monitor none -no-reboot \
  -kernel /vm/vmlinuz -initrd /vm/root.cpio.gz \
  -append 'console=ttyS0 rdinit=/init panic=-1 loglevel=4 systemd.show_status=no module_blacklist=gpio_ich,snd,snd_pcm,snd_hda_intel,snd_pcsp,usbcore' \
  2>&1 | tee "$log"
result=${PIPESTATUS[0]}
set -e
(( result == 0 ))
# The queue fixture writes directly to the guest serial device. A service
# with PrivateDevices emits through journal+console instead; normalize only
# that fixture's known journal prefix, retaining every failure marker.
markers=$(mktemp)
trap 'rm -f -- "$markers"' EXIT
sed -E 's/^\[[ 0-9.]+\] dxb-pat-manager-test\[[0-9]+\]: (DXB_MANAGER_RESULT=)/\1/; s/\r$//' "$log" > "$markers"
[[ $(grep -c '^DXB_MANAGER_RESULT=PASS$' "$markers") == 1 ]]
if grep -q '^DXB_MANAGER_RESULT=FAIL' "$markers"; then exit 1; fi
echo 'Real guest systemd checks passed; hardware and full lifecycle acceptance remain separate.'
