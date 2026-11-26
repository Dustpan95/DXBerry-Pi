#!/bin/bash
set -euo pipefail
log=/evidence/systemd-guest.log
devices=()
boot_args=''
case ${DXB_RECOVERY_PHASE:-} in
  '') ;;
  prepare|verify)
    [[ -f /tmp/update-recovery.img && ! -L /tmp/update-recovery.img ]]
    log=/evidence/update-recovery-$DXB_RECOVERY_PHASE.log
    devices=(-drive "file=/tmp/update-recovery.img,format=raw,if=none,id=fixture,cache=writethrough"
             -device "virtio-blk-pci,drive=fixture,serial=DXB-CI-RECOVERY")
    boot_args=" dxb_recovery=$DXB_RECOVERY_PHASE"
    ;;
  *) exit 2 ;;
esac
memory=${DXB_GUEST_MEMORY_MIB:-2048}
case $memory in 2048|4096) ;; *) echo 'Unsupported guest memory size' >&2; exit 2;; esac
set +e
timeout --signal=TERM --kill-after=5 420 qemu-system-x86_64 \
  -accel tcg,thread=multi -machine q35,usb=off -cpu max -smp 2 -m "$memory" \
  -nodefaults -nic none -display none -serial stdio -monitor none -no-reboot \
  "${devices[@]}" -kernel /vm/vmlinuz -initrd /vm/root.cpio.gz \
  -append 'console=ttyS0 rdinit=/init panic=-1 loglevel=4 systemd.show_status=no module_blacklist=gpio_ich,snd,snd_pcm,snd_hda_intel,snd_pcsp,usbcore'"$boot_args" \
  2>&1 | tee "$log"
result=${PIPESTATUS[0]}
set -e
(( result == 0 ))
# The queue fixture writes directly to the guest serial device. A service
# with PrivateDevices emits through journal+console instead; normalize only
# that fixture's known journal prefix, retaining every failure marker.
markers=$(mktemp)
trap 'rm -f -- "$markers"' EXIT
sed -E 's/^\[[ 0-9.]+\] dxb-(pat|wsjtx)-manager-test\[[0-9]+\]: (DXB_MANAGER_RESULT=)/\2/; s/\r$//' "$log" > "$markers"
[[ $(grep -c '^DXB_MANAGER_RESULT=PASS$' "$markers") == 1 ]]
if grep -q '^DXB_MANAGER_RESULT=FAIL' "$markers"; then exit 1; fi
echo 'Real guest systemd checks passed; hardware and full lifecycle acceptance remain separate.'
