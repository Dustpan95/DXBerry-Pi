# EEPROM tools package maintenance

Status: `rpi-eeprom` upgraded from `28.36-1` to `28.38-1` through DockerPC at
`2026-11-23T18:29:36.739655+00:00`. This updates the tools and firmware-image
catalog. The maintenance did not invoke the firmware updater, stage an EEPROM
update, flash firmware or reboot the Pi. Actual firmware/boot acceptance remains
separate from this package transaction.

## Reviewed boundary

The board is a Raspberry Pi 4 Model B Rev 1.4 (`0xc03114`). Its running
bootloader build remains `2026-05-17T19:13:18+00:00`, newer than the automatic
update minimum `1746717695`. This board revision has no separately updated
VL805 EEPROM in the vendor updater's policy. The default release channel,
service unit and package post-install hook are byte-identical between versions.
The version-selection, automatic-apply and VL805-query function blocks also
remain byte-identical. No relevant manager environment override or service
drop-in was present.

The package hook normally restarts `rpi-eeprom-update.service`, whose command is
`rpi-eeprom-update -s -a`. The transaction installed a temporary, narrowly scoped
`policy-rc.d` that returned 101 for that unit. Its execution log confirms the
restart was denied. Other-unit behavior retained the normal default. The policy
file was absent beforehand and was removed after verification; the updater's
existing enabled/active state and service identity were preserved.

## Retained restoration material

Private material remains at `/root/dxb-eeprom-review-20261123`:

- `original.deb`: the matching cached `28.36-1` archive, SHA-256
  `46f3726ea2d76f836c8368cedf7cb151ce0bd0f4028da39e5d026959285a40b7`.
- `rpi-eeprom_28.38-1_all.deb`: the candidate verified against APT metadata,
  SHA-256 `4ab84569c9f7d5f72a8ef655714e1d4f913390b68bc4f2eb0517901a464c882f`.
- `pre-upgrade/boot-and-package-state.tar.gz`: verified backup of boot files,
  package state, package marks and the EEPROM defaults/service file; its digest
  is in `pre-upgrade/backup-manifest.json`.
- Package inventories, all eight service identities, the complete boot-file
  hash/link inventory, package marks/holds, policy and transaction logs.

The original archive payload matched its checksum inventory, and all 61 installed
payload files/links matched that original before installation. The existing
33-package maintenance backups and earlier local-patch originals remain retained.
This establishes restoration material, not a completed rollback exercise.

## Transaction and verification

Fresh update/settings/provisioning gates and admission locks excluded cooperating
maintenance workers. Record/apply locks were not retained across package work.
A strict repository refresh preceded an exact one-package dependency simulation,
which refused extra, removed, duplicated or changed packages. The verified local
archive was installed with `dpkg --force-confold`; no dependency packages,
autoremove, persistent repository changes or default-channel changes were requested.

The initial APT local-file/`--no-download` attempt refused archive acquisition
before changing any package. The old version and clean package database were
confirmed and the temporary policy removed. A synthetic APT-cache harness failed
in its local-repository setup and was not accepted as validation. The final
direct-local-archive path was instead verified in Docker using a real `dpkg`
upgrade/configure cycle and the byte-matched Debian service helper. The fresh
retry revalidated the original backup and full baseline before proceeding.

Docker checks passed Bash syntax, ShellCheck and Python compilation. Policy
fixtures proved updater denial, preserved other-unit behavior, collision refusal,
normal cleanup and refusal to delete a changed policy file. Plan fixtures parsed
the actual dependency plan and refused extra/removal/duplicate/version/summary
changes. The final local-package fixture completed without network access and
proved the updater call was denied before any `systemctl` execution. These are
software fixtures, not firmware, radio or real-manager delivery acceptance.

Independent post-transaction diagnostics verified:

- Only the selected package version changed; `dpkg --audit` and package payload
  verification passed. Manual marks and holds were unchanged.
- Both retained package archives and the backup archive matched their hashes.
- All seven station/web/database service identities plus the EEPROM service
  identity were unchanged. All 436 boot files/links matched the prior inventory.
- The updater restart was denied, the temporary policy was absent, and no pending
  EEPROM-update marker appeared. No reboot occurred.
- The installed application safeguard/original, `.4` console, checked radio/TX/
  helper/network files, Graywolf configuration and mixer were unchanged. Capture
  and playback PCM streams remained RUNNING.

The durable record is
`/var/lib/dxberry/local-patches/eeprom-package-maintenance-20261123.json`.
Local evidence is retained at `/workspace/dxberry-eeprom-maintenance-tests/`.

## Remaining updates

The refreshed version-2 availability check completed successfully with five
ordinary system upgrades: `base-files`, `sudo`, `parted`, `libparted2t64` and
`wireless-regdb`. Subsequent [restoration archive preparation](system-package-restoration-archives.md)
recovered and authenticated all five originals without changing their installed
versions. The following [five-package maintenance](system-package-maintenance-final-2026-11-23.md)
completed fresh configuration backups, transaction/hook review and installation;
independent checks preserved all eight service identities and the station baseline.
Its fresh availability check reports zero ordinary system upgrades.
The separate WSJT-X backports revision and actual firmware update/boot review
remain deferred. The Pi is not reported as fully updated. Queue activation,
systemd acceptance, capture recovery and physical radio/program workflows remain
separate open items.
