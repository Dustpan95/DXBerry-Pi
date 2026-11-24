# Remaining system package maintenance

Status: the five deferred system packages were upgraded through DockerPC at
`2026-11-23T19:10:48.217382+00:00`. Independent diagnostics passed. No firmware
update, network reset, test transmission or reboot was performed. The console
remains `0.5.0-rc4+idle-reporting.4`; Graywolf remains 0.14.14.

| Package | Before | Installed |
| --- | --- | --- |
| `base-files` | `13.8+deb13u7` | `13.8+deb13u8` |
| `sudo` | `1.9.16p2-3+deb13u2` | `1.9.16p2-3+deb13u5` |
| `parted` | `3.6-5` | `3.6-5+deb13u1` |
| `libparted2t64` | `3.6-5` | `3.6-5+deb13u1` |
| `wireless-regdb` | `2026.05.30-1~deb13u1` | `2026.09.03-1~deb13u1` |

## Restoration material and review

The [authenticated originals](system-package-restoration-archives.md) remain at
`/root/dxb-rollback-archive-review-20261123/originals/`, with signed historical
index evidence and exact package identity/checksum records. Verified candidate
archives and both versions' extracted control metadata also remain retained.
No historical index was added to live APT sources and no trust key was imported.

The fresh backup is
`/root/dxb-final-system-maintenance-20261123/pre-upgrade/configuration-boot-package-state.tar.gz`:
28,382,032 bytes, SHA-256
`afb05d7e85aee530597a0af2f19436c67e1f2225d6d710dc60d5ef74a0e5e7b2`.
It preserves ACLs, extended attributes and numeric ownership for `/etc`, boot
files, dpkg state, APT package marks and the existing root profiles. Its integrity,
member inventory and accompanying state snapshot were checked. Earlier system/
database, application-patch and EEPROM restoration material remains retained.
Private restoration notes explain scoped configuration restoration and caution
against extracting the saved dpkg database over the running package database.
No downgrade, full rollback or spare-media boot exercise was performed.

Installed maintainer scripts matched the originals. Candidate scripts are
identical apart from build-tool comments in the parted packages. Sudo's delivered
authentication/configuration files and the default shell profiles are unchanged.
The upgrade changes `/etc/debian_version` to the candidate's default; the other
protected local configuration files retain their contents, ownership and modes.
The manually selected upstream regulatory database alternative remains selected.
No partitioning command was run.

## Transaction

Validated update/settings/provisioning execution gates and admission locks
excluded cooperating workers. Update/settings jobs and application sessions were
idle, and the manual CODEC remained unregistered. Record/apply locks were not
retained across package or service work.

Preparation and installation each required a fresh strict repository refresh,
candidate SHA-256 metadata checks, an exact five-package dependency simulation
and an unchanged station/package baseline. The plan refused additional packages,
removals, duplicate operations, changed versions or architectures and inconsistent
summary counts. Installation used pinned current-repository versions with local
configuration retention. Any manual marks introduced by explicit APT targets
were restored only for the selected packages; the final manual/hold inventories
match the originals. No autoremove or persistent repository change was made.

Docker checks passed Bash syntax, ShellCheck, Python syntax and seven package-plan
test groups. They covered additional/removal/duplicate/missing operations,
version/architecture changes, malformed plans and changed summary counts. A
separate Docker invocation also accepted the actual prepared Pi dependency plan.
Application source and installed application binaries were unchanged; application
development suites were not repeated for this package-only maintenance.

## Independent diagnostics

A separate inspection verified:

- Only the five selected package versions changed. The package audit and payload
  checks were clear, and the original archives and fresh backup retained their
  verified hashes.
- All eight station/web/database/EEPROM service identities were unchanged, and
  all 436 boot files/links retained their hashes, targets, ownership and modes.
- Protected local configuration, regulatory alternatives, Wi-Fi interface state
  and addresses, manual package marks and holds were unchanged. Sudo's
  configuration syntax check passed.
- The checked radio/TX/helper/network files, Graywolf configuration and mixer
  were unchanged. Capture and playback PCM streams remained RUNNING.
- The station's HTTP and verified HTTPS version endpoints returned
  `0.5.0-rc4+idle-reporting.4`. The boot identity was unchanged, with no
  reboot-required or pending EEPROM-update marker and no temporary service policy.

The durable record is
`/var/lib/dxberry/local-patches/final-system-maintenance-20261123.json`.
Transaction logs, retained-source hashes, backup/state manifests and
`independent-health.json` remain at `/root/dxb-final-system-maintenance-20261123`.
Local evidence is at `/workspace/dxberry-final-system-maintenance-tests/`.

## Fresh availability

The installed version-2 availability checker completed with a fresh strict APT
refresh, no component errors, `cached: false`, zero ordinary system upgrades and
no reboot required. Its retained result is `post-availability.json` in the
maintenance directory. This closes the five-package ordinary maintenance list.

APT still advertises the separate WSJT-X backports revision
`3.0.2+dfsg-2~bpo13+1`, with installed upstream version `3.0.2`. It remains kept
back and outside the ordinary count; its dependency/source and actual native
compatibility review remain open. The station is not described as having every
advertised package revision installed.

System-manager queue acceptance, capture recovery, physical radio/audio/PTT/QSO
workflows, native-program compatibility and firmware/boot/rollback acceptance
remain open. Queue activation remains off. The separate WSJT-X backports revision
is outside this transaction.
