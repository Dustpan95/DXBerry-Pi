# Full provisioning operation lock

Status: implemented in development source, with an isolated maintenance copy
installed on the Pi. The console remains verified `.4`; preset authority remains
disabled and the development generation helper remains uninstalled.

A valid full `dxberry-provision` run now takes an exclusive process-shared lock
at `$DXB_RUN_DIR/provision.lock` before station-state creation, generation
preparation/advance, log setup or configured steps. The default wait is 30
seconds; trusted operator/test configuration can select
`DXB_PROVISION_LOCK_WAIT`. Failure returns 6 before tracking or live work.

The lock is separate from the generation, record, apply and native-release
locks. Unit conditions, preparation/native hooks and emergency STOP do not take
it. Provisioning can wait for those hooks without asking them to acquire its
own lock. An update's provisioning child takes this lock itself; the parent
update job does not acquire it here.

## Lifetime and refusal

The full main function runs in a subshell. Its dynamically allocated descriptor
stays held through the command's work and closes on every return or exit,
including tracking refusal and failed steps. Descendants can retain an inherited
copy longer; the lock is never explicitly unlocked for a still-running child.
The file remains for later runs and is never deleted to clear contention.

The final runtime directory must be a real root-owned directory with mode 0700,
0750 or 0755. A new empty lock is exclusively created with mode 0600. Existing
locks must be root-owned, regular, single-link, empty and mode 0600; unsafe
objects are refused without chmod, truncation or repair. The descriptor opens
read-only. After flock acquisition, the directory identity and the held lock's
identity/metadata are checked again through the configured path.

`--check`, help and already-completed first boot acquire no lock. A first-boot
waiter checks completion again after acquisition, so a run completed by the
previous holder is not repeated. Missing/invalid configuration keeps its earlier
failure/reporting/reboot behavior and is outside this valid-run boundary.

## Verification and remaining integration

Docker fixtures run the actual provisioning main with simulated configured
steps and tracking responses. Actual kernel flock checks refuse a shared reader
and second full run during a delayed first step, then allow a retry after that
step exits with failure. The contender performs no tracking or live-step write.
Read-only validation remains available while the first run holds the lock.

Further fixtures cover acquisition failure, missing flock, directory/file type
and permission refusal, hard links, inode/path replacement while waiting,
descriptor closure on a normal early return and completed-first-boot recheck.
Replacement/fault callbacks are injected; they are not real manager acceptance.
The prior implementation fails all four added regression groups.

The gate by itself proves neither a free radio nor complete lifecycle coverage.
It does not cover all update/settings/record/apply phases, arbitrary direct
component calls, surviving job descendants or privileged/nonparticipating
writers. A future verifier still needs protected ancestor/path validation,
installed source/protocol binding, complete lock ordering and fresh job,
process, unit and restoration-journal evidence. The shell guard's final-object
checks do not replace that reader-side descriptor walk. No production verifier
is constructed by this change, and no physical radio is registered or used.

Source verification evidence is retained at
`/workspace/dxberry-provision-lock-tests/`. The separate Pi installation below
is recorded independently.

All 966 shell tests and the tree check passed in Docker. Final focused lock
checks and repository Bash syntax/ShellCheck passed after fixture isolation
annotations and local variable renaming. No Go/UI source changed, so Go, cross-build and browser checks
were not repeated. Source snapshots use executable placeholders for required
compiled helpers and are explicitly not deployable artifacts.

## Pi maintenance installation

An isolated copy was applied on 2026-11-23 at 11:57:27 UTC. It starts from the
Pi's exact installed provisioner and adds only the tested lock helper, main
subshell/descriptor lifetime and first-boot completion recheck. It adds no
generation-helper invocation and does not install the development tree.

Only `/opt/dxberry/bin/dxberry-provision` was replaced, retaining root ownership
and mode 0755. Its previous SHA-256 was
`5d077924383eb2cd5d5910652cd1c7757c04800781d9b9c082c15c9ce6378d9c`;
the installed SHA-256 is
`88ff5cbf3bc5da89b5972e6e47fa9aa37bf0b19d8d7a7ae4f000394fd0ce0b59`.
The source basis is `eb871267dcc5585241cfdf25042548f73d5935ba`.

The installation record is
`/var/lib/dxberry/local-patches/provision-lock.json`; retained original and
checks are under `/root/dxb-provision-lock-eb87126`. Verified upload/staging is
retained at `/root/dxb-provision-lock-stage`. The installer acquired the new
private runtime gate plus the existing update/settings/record/apply locks,
refused running ordinary provisioner processes and pending jobs, verified exact
source/binary hashes, and synchronized the original backup before atomic
replacement. The empty root-owned runtime gate is mode 0600.

All 877 isolated maintenance shell tests and the focused kernel contention,
Bash syntax, ShellCheck and tree checks passed in Docker. Actual atomic-copy,
rollback and incomplete-rollback fixtures passed there too. An exact source
comparison verified the patch boundary; its staged archive hash was
`a7485c6c98dcee5a5029609ae232d13e535dd8f2ed5c83abf8f4548b36c5e9d2`.

Installation and an independent read-only follow-up verified both installed
and backup bytes, the `.4` console hash/version, unchanged identities of all five
checked services, unchanged radio/TX/helper/network files and mixer, and
unchanged Graywolf configuration rows. Both PCM streams remained RUNNING.
No service was restarted, no radio command issued and provisioning was not run.
The generation helper and epoch directory remain absent. This is reversible
file maintenance with operational health checks, not full provisioning,
systemd workload, RF or audio-quality acceptance. Local evidence is retained at
`/workspace/dxberry-provision-maintenance-tests/`.
