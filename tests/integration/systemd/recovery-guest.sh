#!/bin/bash
set -Eeuo pipefail
finish() {
  local rc=$?
  trap - EXIT
  if (( rc == 0 )); then
    printf '\nDXB_MANAGER_RESULT=PASS\n' > /dev/ttyS0
  else
    journalctl --no-pager -n 30 -u dxberry-netwatch.service || true
    printf '\nDXB_MANAGER_RESULT=FAIL:%s\n' "$rc" > /dev/ttyS0
  fi
  systemctl poweroff --no-block || true
  exit "$rc"
}
[[ $(hostname) == dxb-ci-guest && $(< /etc/machine-id) == 11223344556677889900aabbccddeeff ]]
[[ $(< /proc/1/comm) == systemd && -f /fixture/packages/dxberry-pi-0.0.2.tar.gz ]]
trap finish EXIT
trap 'echo "Disk rehearsal failed at line $LINENO: $BASH_COMMAND" >&2' ERR
phase=$(sed -nE 's/.* dxb_recovery=(prepare|verify)( |$).*/\1/p' /proc/cmdline)
[[ $phase == prepare || $phase == verify ]]
modprobe -a virtio_pci virtio_blk ext4
for ((i=0; i<50; i++)); do [[ -b /dev/vda ]] && break; sleep .1; done
[[ -b /dev/vda && $(< /sys/block/vda/serial) == DXB-CI-RECOVERY ]]
[[ $(blockdev --getsize64 /dev/vda) == 201326592 ]]
disk=/mnt/dxb-recovery
mkdir -p "$disk"
if [[ $phase == prepare ]]; then
  rc=0
  blkid -p /dev/vda || rc=$?
  [[ $rc == 2 ]]
  mkfs.ext4 -q -L DXB-RECOVERY /dev/vda
fi
[[ $(blkid -s LABEL -o value /dev/vda) == DXB-RECOVERY ]]
mount -t ext4 /dev/vda "$disk"
if [[ $phase == prepare ]]; then
  mkdir -p "$disk/opt/dxberry.new"
  cp -a /opt/dxberry "$disk/opt/dxberry"
  tar -xzf /fixture/packages/dxberry-pi-0.0.2.tar.gz -C "$disk/opt/dxberry.new" --strip-components=1
  for tree in dxberry dxberry.new; do
    (cd "$disk/opt/$tree" && find . -type f -print0 | sort -z | xargs -0 sha256sum) > "$disk/$tree.sha256"
    stat -c %i "$disk/opt/$tree" > "$disk/$tree.inode"
  done
  cat /proc/sys/kernel/random/boot_id > "$disk/first-boot"
  # Durable namespace between the updater's old-tree and new-tree renames.
  mv -T "$disk/opt/dxberry" "$disk/opt/dxberry.prev"
  sync -f "$disk"
  echo 'PASS persisted original and candidate at the rename boundary'
else
  [[ $(< "$disk/first-boot") != "$(< /proc/sys/kernel/random/boot_id)" ]]
  mount --bind "$disk/opt" /opt
  [[ ! -e /opt/dxberry && -d /opt/dxberry.prev && -d /opt/dxberry.new ]]
  systemctl start dxberry-netwatch.service
  systemctl is-active --quiet dxberry-netwatch.service
  [[ $(systemctl show dxberry-netwatch.service -p MainPID --value) -gt 1 ]]
  [[ ! -e /opt/dxberry.prev && $(< /opt/dxberry/VERSION) == 0.0.1 && $(< /opt/dxberry.new/VERSION) == 0.0.2 ]]
  for tree in dxberry dxberry.new; do
    [[ $(stat -c %i "/opt/$tree") == "$(< "$disk/$tree.inode")" ]]
    (cd "/opt/$tree" && sha256sum --quiet -c "$disk/$tree.sha256")
  done
  systemctl stop dxberry-netwatch.service
  echo 'PASS fresh boot restored the original tree, started netwatch and retained the staged update'
fi
