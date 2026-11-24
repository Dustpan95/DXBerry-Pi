# Settings request and result binding

Status: implemented with an isolated Pi maintenance installation. Production
preset authority remains unavailable.

Previously, settings used a timestamped unit and an unbound global result, and
published the job document after starting its worker. A fast worker could finish
before that publication, and stale or manual output could appear as another
request's success. Status also read the job file again after probing its unit,
allowing a changed document to inherit the prior unit's state.

## Admission and execution

After validating a real settings change, admission obtains 16 random bytes from
the kernel and validates a nonzero 32-character lowercase hexadecimal request
ID, distinct from the previous request. Entropy refusal returns 6 before writing
settings or taking a network snapshot. Same-clock requests use distinct unit
names. No-op and invalid submissions preserve their existing behavior.

The version-2 job document contains request ID, unit, start time, changed key
names and the network flag. It is atomically replaced with exact readback before
launch. Request ID and unit are passed explicitly through `systemd-run --setenv`.
Publication failure follows the existing failed-launch rollback: restore the
settings/network files, disarm the backstop and remove pending keys. A failed
restore is reported rather than claimed as a successful rollback. A published
request remains recorded if launch fails or its outcome is uncertain.
The existing launch-error rollback does not prove that an uncertain worker has
stopped or cannot write afterward; cancellation and manager acceptance remain
separate work.

The worker validates the exact ID/unit relationship before acquiring its
execution gate or consuming pending keys. Invalid/partial metadata returns 6
without provisioning or changing the previous result. Direct manual apply is
still usable, but publishes an explicitly unbound result with null ID/unit.
Valid workers echo their ID/unit with version, exit and finish time. Atomic
replacement/readback failure after work returns 8 without inventing completion.
This is process-visible publication with no new media-durability guarantee.

## Result presentation

Status captures one job document and validates its unit before probing it.
It constructs its answer from that capture and checks the job file again before
returning; changed or missing evidence yields `job: null`.

An inactive unit can be shown completed only when the single result document
has version 2, the same request ID and exact unit, an integer exit code in 0–255
and a nonnegative integer finish time. A nonblocking shared lease on the existing
valid settings execution gate must succeed. A worker still holding its gate for
EXIT cleanup cannot appear completed. Legacy, malformed, multiple, unrelated,
missing or unavailable results leave completion unknown (`ended`, null exit).
An active unit remains `running`. Public output retains its existing fields and
omits internal version/ID fields; the unit already contains its request ID.

Confirm/revert retain their existing short settings lock and do not acquire the
execution gate or require these documents. Network editing remains disabled by
default. Request identity is cooperative admission evidence, not proof of a
systemd invocation or authority for radio control. Nonparticipating writers,
manual restarts, descendants, complete caller exclusion, protected reader paths
and installed profile/protocol binding remain separate requirements.

## Verification

Docker fixtures exercise the production CLI/library with simulated provisioner
and manager callbacks, real file replacement and kernel flock. Seven regression
groups cover stale/legacy/malformed/multiple results, worker identity and manual
apply, same-clock requests and a synchronous fast worker, entropy failures,
request publication rollback (including temporary network files), failed launch,
failed result publication, a job changing during status, and held/unsafe gates.
All seven fail on the previous command/library and pass on the new pair.

The existing job-presentation tests now use the real worker with request metadata
and simulated provisioning to generate successful/failed results. No Go or UI
source changed. Fixtures establish software behavior; actual systemd workload,
network recovery, RF and audio acceptance remain open. Evidence is retained at
`/workspace/dxberry-settings-result-tests/`.

All 990 development shell tests and all 901 isolated maintenance shell tests
passed in Docker with zero failures. Both tree checks, repository Bash syntax,
ShellCheck and installer atomic-replacement/rollback fixtures passed. No Go or
browser checks were repeated for this shell-only change.

## Pi maintenance installation

Installed through DockerPC at `2026-11-23T13:10:22Z`, from source
`c919d412171bbe8e3be8491236191b9322f6fa1d`. The tested command and library
match development exactly. Removing only the three additive metadata helpers
restores the saved installed library byte for byte. All three installer
execution/provisioning guards match the installed baseline.

| File under `/opt/dxberry` | Previous SHA-256 | Installed SHA-256 |
| --- | --- | --- |
| `bin/dxberry-config` | `349a0c308aadaed1297740ce600db7f0bfb6d7fc3ee8b5207851f6c21f0e7615` | `e0023ed1f7a70ec280d5104ee78f1bb968fc657e1f8ba9815648418cb117e3b5` |
| `lib/settings.sh` | `c431914c462d88c85f024fc9fc247be9dd7e24ed2f691dc68d422b6712cd19f2` | `741509d1a1f7b86147c6d31204a663c8bedc56afb15c497e6eff98720c9f6d67` |

Root ownership and modes 0755/0644 were preserved. The record is
`/var/lib/dxberry/local-patches/settings-result-binding.json`; originals and
checks remain at `/root/dxb-settings-result-c919d41`, with staging retained at
`/root/dxb-settings-result-stage`. This supersedes the installed command hash
in the earlier settings execution-lock record while preserving its guard.

The 14,957-byte archive had SHA-256
`656bf368db685f79bddec3fefeef6b204ae05f1198e7cc7d52b57587a6416b03`.
All four upload pieces, the archive and staged bytes/modes/source identity were
verified. Installation retained and synchronized originals, installed the
additive library first and then atomically replaced the command. Rollback
restores the old command before its library. The installer acquired update,
settings and provisioning execution gates before admission/record/apply locks,
required no pending/active jobs and pinned baseline and `.4` console hashes.

Independent read-only diagnostics verified installed/backup hashes, unchanged
identities of all five services, previous maintenance records, radio/TX/helper/
network files, mixer and Graywolf configuration tables. Both PCM streams stayed
RUNNING, and HTTP/verified HTTPS version checks passed. Installed settings status
returned valid JSON with no pending change or job. No settings/update job,
provisioning run, service restart, radio command or tracking-helper installation
was performed. Real manager workload and hardware acceptance remain open.
