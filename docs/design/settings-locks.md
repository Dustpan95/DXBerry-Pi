# Required settings and update locks

Status: the isolated two-script patch is installed through DockerPC at
`2026-11-23T09:17:18Z`. No service restarted. The Pi retains its `.4` console,
working radio configuration and separate required radio-lock patch.

Settings and update commands previously continued when `flock` was unavailable
or their lock file could not open. That allowed concurrent settings changes or
update starts to pass the intended serialization boundary.

All three helpers now refuse work if the run directory cannot be created, the
lock command is missing, the lock file cannot open or bounded acquisition fails.
They return the existing exit code 5. Contention keeps its existing retry message;
failed acquisitions close the newly opened descriptor. Successful operations
retain the existing descriptor, wait limit and lock scope.

| Boundary | Lock | Descriptor |
| --- | --- | --- |
| Update/install/removal command start | `update.lock` | 6 |
| Update job start versus settings changes | `config.lock` | 7 |
| Settings set/confirm/revert | `config.lock` | 7 |

The settings job also uses the same mandatory helper before rearming a network
undo timer. Failure retains its existing diagnostic and earlier timer behavior.
This change does not introduce a lock across a whole asynchronous job, serialize
nonparticipating writers or provide the complete exclusion needed by preset
recall. Emergency PTT STOP has no dependency on these helpers.

## Verification

All 946 development and 873 isolated maintenance shell tests passed in Docker,
along with repository Bash syntax, ShellCheck and both tree checks. Installer
fixtures verified atomic replacement, exact bytes/modes, complete restoration
after a failure following both replacements, and explicit failure with retained
diagnostics when restoration is incomplete. No Go or browser code changed.

Docker fixtures exercise the actual command dispatch and update-job boundary
with downstream mutation markers. Five entry points each cover missing command
support, an unusable run directory, an unopenable lock file and failed
acquisition. No marker may run and no new lock descriptor may remain open.
Separate fixtures hold real kernel locks, check contention for all three
helpers, release them, and verify that each helper then acquires and retains
its lock. Services, radio devices and RF are outside these fixtures.

The initial full runs exposed a fixture-only collision between a fixed holder
descriptor and Bash's saved error-output descriptor. A focused reproduction
with the runner's error redirection failed in the same way; dynamically
allocating the holder descriptor fixed it. The production helpers did not
change for that correction.

The isolated maintenance candidate changes only these three helpers and adjacent
comments in the installed `.4` source. It excludes unfinished generation,
preset and native-program functionality. Installation requires exact original
hashes/modes, the known console binary, no pending settings/update job, empty
registered radio inventory and update/configuration/record/apply locks.
Original scripts are retained; each replacement uses fsync and atomic rename.
Rollback attempts both restores and verifies their original hashes. An
incomplete rollback retains its diagnostic record and reports failure.

Operational verification compares service identities, Graywolf configuration,
mixer and radio/TX/helper/network hashes and checks local HTTP and certificate-
verified HTTPS health. It does not replace physical radio or systemd lifecycle
acceptance. No service restart, retuning, registration or RF test is required.

## Installed files and rollback

Source commit: `5ef2c934dd82c21c480f01918a5b1b49d7a77b10`. The Docker-built
archive is 19,480 bytes with SHA-256
`f0abe4a73a1c0eb5b77459c847c3850d372e2229cd00a7cbd2e23822af8e2246`.
Both transfer pieces, the complete archive and staged script hashes were
verified before installation. Each maintenance helper exactly matches its
tested development implementation; the remainder of each installed script
comes from the retained maintenance source.

| Installed path under `/opt/dxberry` | SHA-256 |
| --- | --- |
| `bin/dxberry-config` | `06c113177d0c3b1af64d2be82d1930ec85e0a66422a45f7f58e185c897f45e30` |
| `lib/update.sh` | `c3c88ab7a7b25b13745415c038ab51accacc541fb7639de2b6437137cfb945b2` |

The installation record is
`/var/lib/dxberry/local-patches/settings-locks.json`. Originals are retained at
`/root/dxb-settings-locks-5ef2c93` with the same two relative paths. To roll back,
take the update/configuration/record/apply locks, restore both originals
atomically with their retained modes, and verify `original.sha256` before
retiring the new record. Retain all backups; no configuration restore or service
restart is needed.

Installation and independent postchecks verified installed and backup hashes,
file ownership/modes, unchanged identities for all five services, unchanged
radio/TX/helper/network/boot files and mixer, and both CODEC streams RUNNING.
The installer also compared Graywolf configuration tables and checked local
HTTP and certificate-verified HTTPS health. The console remains
`0.5.0-rc4+idle-reporting.4` with SHA-256
`4d35861f720c58b4a86147f9c0265d3c4c2a1976946edbbfe107fdbbedb5cf14`.
The generation helper and its state remain absent on the Pi. Capture diagnosis,
complete preset authority and physical radio acceptance remain open.

Evidence is retained at `/workspace/dxberry-settings-lock-tests/`.
