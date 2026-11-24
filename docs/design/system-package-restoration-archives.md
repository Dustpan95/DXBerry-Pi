# System package restoration archives

Status: original archives for the five deferred system packages were recovered
and verified through DockerPC on November 23, 2026. Their installed versions
remain unchanged. This resolves the missing-archive prerequisite; configuration
restoration preparation and transaction review remain required before upgrading.
No downgrade or full rollback exercise was performed.

This note records the preparation result. The subsequent
[five-package maintenance](system-package-maintenance-final-2026-11-23.md)
completed configuration backups, hook review and installation, with independent
diagnostics preserving the station baseline. The originals described here remain
retained for restoration.

## Retained originals

Private restoration material is retained on the Pi at
`/root/dxb-rollback-archive-review-20261123`. Its `originals/` directory contains:

| Package | Retained installed version | Architecture | SHA-256 |
| --- | --- | --- | --- |
| `base-files` | `13.8+deb13u7` | arm64 | `5330362213dc8b9327a56383104235c269797d5db2fd8688f535298d4c44e4d2` |
| `sudo` | `1.9.16p2-3+deb13u2` | arm64 | `907d333f72f3369b2ceda64dbcfccf0dc515543b76ac68d5c901e309f118709b` |
| `parted` | `3.6-5` | arm64 | `62006921ec2a8b10ad3e52d0bbaf41b31f6806ea5d3ee15c8544b379c6e712ab` |
| `libparted2t64` | `3.6-5` | arm64 | `cc441a27a61a0d7320cc991f147049b12444c6e290277f1a0a5a3fc5942ded63` |
| `wireless-regdb` | `2026.05.30-1~deb13u1` | all | `72048ee2f369a8c2df8b5f99f79489d3812d869282038cfe55e23a4748231399` |

The archives came from Debian Snapshot. Discovery API results alone were not
accepted as authentication. Each retained archive has a verified chain from an
InRelease signature, checked using the installed Debian archive keyring and
`sqv`, through the signed SHA-256/size of `Packages.xz`, to the exact package,
version, architecture, filename, size and SHA-256. The archive control identity
and its MD5 inventory also match the installed package inventory. A separate
inspection reverified signatures, metadata/index hashes and all five archive
hashes; installed payload verification reported no differences.

The historical indexes were `trixie-proposed-updates` at the original upload
snapshots for `base-files`, `sudo` and `wireless-regdb`, and `sid` at the original
upload snapshot for `parted` and `libparted2t64`. These are restoration records
for exact versions already installed. They were not added to live APT sources
or used as current upgrade catalogs. TLS and signature verification remained
enabled; no new trust keys were imported.

The first stable-index lookup correctly refused an index that did not contain
the exact installed version. A pool upload can precede its stable-index entry.
Only an absent exact package or an HTTP 404 permits trying another explicitly
named historical suite. Signature failures, checksum mismatches, ambiguous
records and other download failures stop preparation.

`originals-verified-v2.json` records the full chain and retained archive paths.
Per-package directories retain InRelease, authenticated Release, the compressed
index, signature log and archive control metadata. `independent-verification.json`
records the separate inspection. Local preparation evidence is retained at
`/workspace/dxberry-rollback-archive-tests/`.

## Candidate preparation and remaining work

The five currently advertised candidates were downloaded into the private
`candidates/` directory and checked against APT SHA-256/size metadata and archive
package/version/architecture fields. Their control scripts and configuration
inventories are retained in `candidates-reviewed-metadata.json`. This preparation
used the existing APT catalog; a fresh strict repository refresh and exact
dependency simulation are still required for an installation transaction.

Before installation, retain a fresh configuration/package-state backup and
review both upgrade and restoration hooks. Specific boundaries include:

- `base-files`: merged-/usr checks and diversions, release identification files,
  and conditional updates to the default shell profiles.
- `sudo`: sudoers ownership/mode, authentication configuration, tmpfiles and its
  SysV init hook. Preserve the working administrative connection and existing
  local configuration.
- `parted` and `libparted2t64`: documentation-path migration, library diversions
  and the linker-cache trigger. No partitioning operation is requested.
- `wireless-regdb`: firmware database alternatives and their existing selection.
  Preserve the station's network configuration and connection.

These scripts have been collected for review; no package installation or hook
execution was performed during this preparation. The existing update/settings/
provisioning gates, application admission checks, retained backups and exact
package-plan refusal remain required for the eventual transaction.

## Station verification

Independent diagnostics reconfirmed the installed EEPROM tools maintenance
baseline: all eight service identities and all 436 boot files/links were
unchanged; no firmware updater or reboot occurred. The checked radio/TX/network
files, Graywolf configuration, mixer, manual package marks and holds were
unchanged. Capture and playback streams remained RUNNING, and the console
remained `0.5.0-rc4+idle-reporting.4`. No test transmission was issued.

Seven Docker parser test groups passed, including malformed/duplicate records,
wrong identities, unsafe paths, invalid checksums and the distinction between
an absent package and ambiguous matches. Both verifier sources parsed in Docker.
These software checks and the Pi diagnostics do not establish physical radio,
native-program, firmware-boot or full rollback acceptance. Queue activation
remains off; the separate WSJT-X backports revision remains deferred.
