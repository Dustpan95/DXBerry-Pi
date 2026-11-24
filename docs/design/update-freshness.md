# Update availability freshness

Status: implemented and installed as isolated Pi maintenance. This changes
availability reporting and its
cached summary, without installing packages or changing the upgrade worker.

The previous checker warned after a failed package-list refresh, then simulated
an upgrade using old lists. A successful empty simulation could consequently
report the system as up to date. APT can also return success after individual
repositories fail, retaining older indexes. Debian-sourced program comparisons
used those same candidates without knowing whether refresh succeeded.

## Fresh checks and partial failure

The system checker now uses `APT::Update::Error-Mode=any`. If refresh fails, it
returns an explicit error and no package count, and never simulates the old
lists. Simulation failure retains its existing unavailable result. The parent
passes availability success explicitly to its program checker; the default
is unknown for Debian programs. A failed system check leaves their installed
versions visible with an error, without inventing latest/update values.
Independent upstream program checks continue, and a subsequent successful
invocation does not inherit failure state.

Availability caches now use version 2. The checker reuses only one current
version object with a bounded numeric timestamp; legacy, future-version or
malformed documents force a fresh check. Good answers retain the existing
six-hour TTL; partial errors use the existing shorter error TTL. This cache
version is separate from job request/result identity and grants no authority.

The release-summary reader accepts only a single version-2 document. Legacy
cache summaries are unknown until the Updates check refreshes them. Missing or
invalid boolean availability and nonnegative integer package counts remain
null instead of defaulting to false/zero. Status reads do not run APT or make
network requests. Existing UI fields and error presentation are retained.

## Verification

Five Docker regression groups cover failed refresh with an otherwise empty
simulation, partial repository failures requiring strict mode, Debian versus
upstream availability and recovery, legacy/unknown cache migration, and a
read-only status summary rejecting legacy/missing values. All five fail against
the previous library pair and pass against the new functions.

A separate real-APT Docker fixture uses only an unreachable private-loopback
repository, isolated APT configuration/cache/state, a read-only root, no
capabilities, no external network and public read-only source mounts. Default
refresh exits 0 after that repository failure; strict mode exits 100. The real
production checker then returns an unknown/error report without simulation.
The previous function fails that expectation. No package is installed and no
service or radio action runs in this fixture.

The initial full suites exposed one page-contract fixture still writing the
legacy cache format (three missing-field assertions). That fixture and existing
TTL/status fixtures now publish version 2 for their successful-cache scenarios;
dedicated regressions retain the legacy refusal cases. No UI source changes.
Evidence is retained at `/workspace/dxberry-update-freshness-tests/`.

The corrected full Docker runs passed 1,000 development and 911 isolated
maintenance shell tests with zero failures. Both Bash syntax/ShellCheck and tree
checks passed. No Go or UI source changed, so their broader checks were not
repeated. The real-APT fixture's first launch could not read private source
mounts without capabilities; public read-only file copies corrected that setup
without weakening its read-only/no-capability/no-network boundaries.

## Routine Pi maintenance review

The November 23 check reported 39 system-package update candidates. Separate
Graywolf/DXBerry and installed-program checks reported no newer release; Debian
candidate freshness must still meet the stricter check above. No package upgrade
has been performed as part of this reporting change.

The package inventory identified cached or advertised original versions for
34 candidates. Five originals were neither cached nor advertised by current
repositories: base-files, sudo, parted, libparted2t64 and wireless-regdb. EEPROM
tooling is reviewed separately because its package script restarts the EEPROM
update service. Original-package staging therefore selects the other 33
candidates, verifies package/version/architecture and retained hashes, and
performs no installation or restart. Staging completed with 33 retained original
packages whose package/version/architecture and copied hashes were verified,
at `/root/dxb-system-maintenance-20261123-1`. Availability or retention of an
original version is not a completed rollback exercise.

Before any package upgrade, complete current configuration/database backups,
retain and verify the original packages, review an exact dependency plan and
require a successful strict repository refresh. Firmware and packages lacking
a retained original need their own restoration plan. Existing radio/TX settings,
manual CODEC/Graywolf registration and local maintenance patches must be
preserved; a published whole-tree update must not overwrite them incidentally.

## Pi maintenance installation

Installed through DockerPC at `2026-11-23T14:18:52Z`, from source
`8558d8695d3c3ccdb8f8631f2aa604ea7e2e942c`. Only three availability-check
functions in the update library and the status-summary reader were replaced
on the exact installed baselines. Development-only generation hooks remain
excluded, and no tracking helper/epoch directory was installed.

The first installer refused a status-library hash mismatch before creating a
backup or replacing either target. The older maintenance snapshot lacked the
existing serialized status-collector flow. The live file matched the canonical
pre-change source; its collector flow was preserved byte for byte outside the
reader change. The corrected isolated source passed all 911 maintenance tests,
Bash syntax/ShellCheck and tree checks again. The refused package/staging remain
retained; its earlier test result was not accepted as installed-baseline proof.

| File under `/opt/dxberry` | Previous SHA-256 | Installed SHA-256 |
| --- | --- | --- |
| `lib/update.sh` | `9e404d0c6385164dce28c6e704f45b65a10ce7ef39ee9583c4aaffc9bb77d48a` | `9dce0fc05c827a0d649956add2ddc44d46b11277c88d677ca19a7fe8c72aa790` |
| `lib/status.sh` | `c562b61c5feee035b0c7545722f34f489da30a15d96fb910dffe36331c0f6362` | `1e95fbc41a0675d78fe49f5136a985261d61108ac039edc28949028f0bbba7b8` |

Both libraries remain root-owned mode 0644. Originals and verification are
retained at `/root/dxb-update-freshness-live-8558d86`; staging remains at
`/root/dxb-update-freshness-live-stage`. The installation record is
`/var/lib/dxberry/local-patches/update-freshness.json`. This supersedes prior
installed hashes for these two libraries while retaining earlier records.

All seven transfer pieces, the 25,199-byte archive (SHA-256
`103add8b449abc364dcba3a5900620e2c42688148f35b989fcdc649e83ae818a`),
staged modes and source hashes were verified. The installer required the exact
old library/console hashes and no active or pending jobs; it retained originals
and used fsync/atomic rename. The restrictive reader was installed before the
producer; rollback restores the producer before its reader.

Independent read-only diagnostics verified installed/backup hashes, all five
unchanged service identities, checked radio/TX/helper/network files, previous
records, Graywolf configuration and mixer. Both PCM streams stayed RUNNING;
HTTP and verified HTTPS version checks passed. No service restart, radio command,
package installation or provisioning run occurred during this library patch.
Real manager and hardware acceptance remain separate.

The subsequent operational check used the installed version-2 strict refresh
and confirmed 39 system-package candidates, with no Graywolf/DXBerry/program
availability errors and no reboot required. Its evidence is retained at
`/root/dxb-maintenance-check-20261123-2`. Package maintenance has its own backup,
dependency and outcome record; this cache result is not evidence that an upgrade
has already run.

The later [system maintenance transaction](system-maintenance-2026-11-23.md)
completed 33 separately reviewed package upgrades. Its post-upgrade strict check
reported six ordinary system candidates. APT separately keeps back a WSJT-X
backports package revision. The transaction and its independent health evidence
are recorded separately from this library installation.

The later [upgrade preparation patch](update-worker-preparation.md) also requires
strict refresh and successful simulation in the worker. Its installation record
supersedes this update-library hash while leaving the status library unchanged.
