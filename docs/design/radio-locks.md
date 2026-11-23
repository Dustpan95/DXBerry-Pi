# Required radio mutation locks

Status: the minimal four-script maintenance patch is installed through DockerPC
at `2026-11-23T07:02:17Z`. The Pi retains the separate `.4` console binary.
No service restarted and the unfinished preset/native-program features remain
excluded from the maintenance source.

Radio changes, program start/stop and program removal previously continued
without serialization when `flock` was missing or the record lock could not
open. Radio apply had the same fallback for its own lock. Two concurrent
operations can read different desired records, overwrite ownership or
interleave device/service changes; apply being repeatable does not make that
interleaving safe.

## Behavior

All four lock helpers now require an available lock command, a usable run
directory, an opened lock file and a successful bounded acquisition. A failure
returns before loading/mutating the radio record or calling the downstream
start/stop, removal or apply implementation. Failed acquisitions close their
new descriptor. Normal success retains the existing lock scope and wait limit.

| Operation | Lock | Failure exit |
| --- | --- | --- |
| Radio add/set/remove/claim/release | `record.lock`, descriptor 8 | 6 |
| Program start/stop | `record.lock`, descriptor 8 | 5 |
| Program removal job | `record.lock`, descriptor 8 | 5 |
| Radio apply/hotplug | `apply.lock`, descriptor 9 | 6 |

An ordinary radio change can still reach a later apply failure after the record
was saved; existing partial-change behavior remains. A lock failure at the
outer record boundary prevents that operation from beginning. Diagnostics say
which lock is unavailable; contention retains the existing retry message.

Read-only screen/status operations keep their existing behavior. The session
unit's prepare/present/stopped hooks do not gain a record lock: a parent program
start/stop can already hold that lock while waiting for the unit hook. Adding
it there would deadlock. The console's emergency PTT STOP path is separate and
unchanged; a program-stop command still requires the record lock as before.

## Evidence and remaining authority

Docker refusal fixtures exercise the actual radio CLI, program session-control,
removal and apply boundaries. Missing command support, a directory in place of
the lock file, an unusable run directory and rejected acquisition must all
prevent the downstream implementation from running and leave no new descriptor
open. Existing concurrency, retry, program and hotplug fixtures remain relevant
for the successful path. Fixtures substitute programs, services and devices;
they are not systemd or physical-radio acceptance.

All 934 development shell tests passed in Docker, along with repository Bash
syntax, ShellCheck and the tree check. The five focused refusal tests cover
20 operation/failure combinations. A separate installer fixture verifies bytes
and modes after atomic replacement, restoration after a failure following two
replacements, and explicit failure/diagnostic retention if rollback is incomplete.
The first red run reproduced unguarded downstream work and leaked descriptors;
its contention diagnostic assertion was then adjusted to retain the existing
retry wording. No Go or browser code changed.

The isolated maintenance source passed all 871 shell tests, Bash syntax,
ShellCheck and its tree check. Its initial run had one usage-test failure:
the inherited test expected the development-only `verify-present` command,
which this maintenance source intentionally excludes. The unchanged baseline
reproduced that exact failure. The maintenance-only test now requires that
command to remain absent; its targeted check and the full rerun passed. No
production command was added to satisfy the test. Four runtime functions and
adjacent comments, plus four test files, differ from the retained `.4` source.
All four runtime functions exactly match the tested development implementation.

## Installed maintenance and rollback

Source commit: `bea5be74c17623bcffb7a73af463b6eceb07f55d`. The 39,756-byte
archive was built in Docker. Both transfer pieces, the full archive
(`0b4773cbfdd8587085e8bd1a8d3991f0e99624b4db8dae6d30717716aa4379a4`)
and the staged script hashes were verified before installation. The installer
started once and exited successfully. Its four scripts are:

| Installed path under `/opt/dxberry` | SHA-256 |
| --- | --- |
| `bin/dxberry-app` | `7fc3e0530407807e00c3827e2f75fc40e6b832e9c49f6774ec5a5268711e1806` |
| `bin/dxberry-radio` | `f13dbf86dd78dec5daa995be9226e5bd448bcc7164a27e22de992f232891a2a7` |
| `lib/apps.sh` | `a28255023e5e4771224fc52231bfa09bda7bb4e5c291ec774f941a4fb7ec9c7b` |
| `lib/radio.sh` | `781a8ed111c423a3f266d38151d2b54a1b08df8ce45603a3c9642760dc4bb654` |

The subsequent [publication-phase maintenance](radio-publication-lock.md#pi-maintenance-installation)
supersedes the installed `lib/radio.sh` hash only, retaining this record and all
originals. The other three script hashes above remain unchanged.

The update required exact original script hashes/modes, the known `.4` console
binary/version, empty registered inventory and update/configuration/record/apply
locks. It retained originals and replaced each file with fsync and atomic rename.
The installation record was published only after all checks passed. Independent
postchecks verified new and backup hashes, root ownership/modes, unchanged
identities for all five services including the console, unchanged mixer and
radio/TX/helper/network hashes, and both CODEC streams RUNNING. The installer
also compared Graywolf configuration tables and checked HTTP and certificate-
verified HTTPS health. No radio was registered or RF request issued.

The record is `/var/lib/dxberry/local-patches/radio-locks.json`; originals are
retained under `/root/dxb-radio-locks-bea5be7` with the same four relative paths.
To roll back, take the same update/configuration/record/apply locks, restore each
original atomically with its retained mode, and verify `original.sha256` from
that backup before retiring the new record. No service restart or configuration
restore is needed. Keep all backups. A failed install attempts all four restores
and reports whether the original hashes were verified; an incomplete restoration
requires inspection rather than a claim of successful rollback.

The console remains `0.5.0-rc4+idle-reporting.4` with binary SHA-256
`4d35861f720c58b4a86147f9c0265d3c4c2a1976946edbbfe107fdbbedb5cf14`.
The independent capture fault and physical RF/audio acceptance remain open.

## Remaining authority

These changes remove permissive fallback; they do not supply fresh program-free
evidence or a continuous lifecycle generation. They do not harden the lock path
against privileged replacement, make non-cooperating writers participate, or
serialize the intentionally separate unit hooks. The preset `TargetSource`
still needs its own complete lifecycle/identity protocol. The cached program
holder and an inactive systemd snapshot cannot establish that no intervening
program invocation occurred. Production preview remains unavailable.

Evidence is retained at `/workspace/dxberry-radio-lock-tests/`.
