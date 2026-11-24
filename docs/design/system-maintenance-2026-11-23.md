# System maintenance — November 23, 2026

Status: 33 system packages upgraded through DockerPC, completed at
`2026-11-23T14:42:20.901805+00:00`. No reboot was performed. The console remains
`0.5.0-rc4+idle-reporting.4`; Graywolf remains 0.14.14. This maintenance does not
close the capture investigation or physical radio acceptance.

## Preparation and transaction

Retained originals for all 33 selected packages were checked against their
installed package names, versions, architectures and SHA-256 hashes. Private
backups remain on the Pi at `/root/dxb-system-maintenance-20261123-1/pre-upgrade`:
configuration, firmware files, the installed application tree and package state;
an all-database MariaDB dump; a consistent Graywolf SQLite backup; and service,
package, configuration, mixer and package-mark snapshots. Archive and database
dump integrity were checked. These checks establish retained restoration
material, not a completed rollback exercise.

A strict repository refresh and a second exact dependency simulation preceded
installation. The transaction pinned the 33 reviewed versions, refused removals
or additional packages, retained changed configuration files and preserved the
installed local application patches. Existing update/settings/provisioning gates
and admission exclusion were retained. Record/apply locks were not held across
service-dependent package work. No radio registration, provisioning or test
transmission was performed.

| Component | Before | Installed |
| --- | --- | --- |
| MariaDB packages | `1:11.8.6-0+deb13u1` | `1:11.8.9+ds-0+deb13u1` |
| nginx | `1.26.3-3+deb13u10` | `1.26.3-3+deb13u11` |
| PHP 8.4 packages | `8.4.27-1~deb13u1` | `8.4.28-1~deb13u1` |

The remaining selected packages include Mesa, Kerberos and other supporting
libraries, rsync and database dependencies. The complete old/new version and
original-package hash inventory is retained in `result.json` in the maintenance
directory and `/var/lib/dxberry/local-patches/system-maintenance-20261123.json`.

## Independent verification

A separate read-only inspection verified all 33 installed versions, unchanged
versions for all other packages, retained original-package hashes and a clear
`dpkg --audit`. Manual package marks and holds were unchanged.

The identities of `dxberryd`, `graywolf`, `dxberry-netwatch` and
`dxberry-logbridge` were unchanged. Those four services plus nginx, MariaDB and
PHP-FPM were active; the web/database services restarted for their updates.
Checked radio/TX/helper/network files, Graywolf configuration and the mixer were
unchanged. Capture and playback PCM streams were RUNNING. MariaDB reported
`11.8.9-MariaDB-0+deb13u1 from Debian`. Station HTTP and verified HTTPS version
checks passed; Wavelog's verified HTTPS endpoint returned its expected 302 login
redirect. No reboot-required flag was present. Login/contact workflows, browser
audio quality and RF behavior remain separate acceptance items.

Preparation and transaction scripts passed Docker syntax, ShellCheck and
package-metadata checks. Docker policy fixtures parsed the actual dependency
plan and refused extra, removed and duplicate/changed packages. Those fixtures
are distinct from the actual Pi transaction and its independent health evidence.
Local evidence is at `/workspace/dxberry-system-maintenance-tests/`.

## Remaining availability

The following six-count inventory records the first transaction's result.
Subsequent [EEPROM tools package maintenance](eeprom-package-maintenance.md)
upgraded `rpi-eeprom` without invoking its updater or changing boot files. Its
fresh availability check reduced the current ordinary count to five; actual
firmware flashing and boot acceptance remain deferred.

Later [restoration archive preparation](system-package-restoration-archives.md)
recovered and authenticated the five missing original packages. Their installed
versions were initially retained pending configuration restoration and transaction
review. The following [five-package maintenance](system-package-maintenance-final-2026-11-23.md)
completed those prerequisites and upgraded them with independent health checks.

The installed strict version-2 availability checker completed successfully after
the transaction, with six packages in the ordinary upgrade simulation:

- `base-files`, `sudo`, `parted`, `libparted2t64` and `wireless-regdb`: their
  original installed versions were neither cached nor advertised by the current
  repositories. Retain an adequate restoration plan before upgrading them.
- `rpi-eeprom`: its package script restarts the EEPROM update service, requiring
  separate firmware and boot restoration review.

APT also lists `wsjtx` from stable-backports as an available package revision
(`3.0.2+dfsg-2~bpo13+1`, installed `3.0.2`) but keeps it back in the ordinary
upgrade simulation. It is outside the six-count system report and was not
changed. Its source/dependency and native-program compatibility review remains
separate. No autoremove or repository changes were made.

The successful post-upgrade check is retained at
`/root/dxb-maintenance-check-20261123-3`; package state, independent health and
mark comparisons remain in the maintenance directory. The station is not being
reported as fully updated.
