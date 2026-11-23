# Radio pin metadata matching

Status: implemented and installed as isolated Pi maintenance. Full handover
exclusion and production preset authority remain open.

Radio registration records a function's USB path, VID/PID and serial. The
presence resolver previously matched only path and function kind, choosing the
first observation. A replacement at the same port could therefore appear as
the recorded radio and pass the initial claim check. The same path-only lookup
could capture that replacement's ALSA name as an original restoration name.

The resolver now requires exactly one observation at the recorded path/kind,
then matches each recorded USB identity field. A recorded empty serial is an
exact value. Missing, malformed or different observed metadata cannot satisfy a
recorded field; a recorded VID/PID must have USB ID syntax and a recorded serial
must be a string. An absent, empty or non-string kernel name is unknown. A mismatch
returns no kernel name, so the existing presence check treats that function as
absent and the initial claim refuses it before starting/wiring an owner.

Presence/runtime resolution and original ALSA-name capture pass the recorded
pin to this matcher. Optional `card_id` does not affect USB matching. Identical
recorded metadata at the same port still matches after kernel renumbering.
Legacy pins without identity fields retain their existing path-only behavior;
they do not become trusted preset evidence.

This matches scanner metadata, without establishing unique physical identity
for identical devices or retaining an opened-device observation. It does not
change udev's location-based naming rules, prove fresh state after a service
wait, or close scan-to-use races. The broader [handover phases](radio-mutation-phases.md),
pending intent, hotplug reconciliation and protected reader/installed-protocol
binding remain necessary. No whole-operation apply lock is introduced; session
prerequisites and emergency cleanup retain their existing independence.

## Verification

Five Docker groups exercise changed VID/PID/serial at the same port, a recorded
empty serial and original-name capture, malformed recorded fields, missing or
ambiguous scanner metadata, and kernel renumbering/legacy compatibility.
Four refusal groups fail against the previous library; the compatibility group
passes before and after. The initial four-group run reproduced three failures;
the final expanded five-group run reproduces four. Changed-port claim fixtures
also verify that no owner is started or wired and the desired owner stays empty.
The scanner reads synthetic sysfs files and service actions are fixtures; this
does not establish real manager, hardware, audio or RF acceptance.

Evidence is retained at `/workspace/dxberry-radio-pin-identity-tests/`.

The first full runs exposed old audio/session fixture records pairing one USB
identity with a different synthetic device. Their recorded IDs and serials now
match the intended two-DigiRig scene; the refusal regressions still exercise
replacements explicitly. Nine focused affected tests passed after correction.
An initial maintenance fixture copy also brought in development-only helper
expectations. Its original maintenance expectations were restored, and package
verification now checks that only the intended USB setup fragments differ
from the baseline audio/session fixtures. A clean full maintenance run is
required after those corrections.

The final Docker runs passed 1,007 development and 919 isolated maintenance
shell tests with zero failures, plus Bash syntax, ShellCheck and tree checks in
both snapshots. The handover prerequisite-ordering regression passed in both.
Atomic replacement and complete/incomplete rollback fixtures also passed. No
production Go or UI source changed, so their broader suites were not repeated.

## Pi maintenance installation

Installed through DockerPC at `2026-11-23T15:32:58Z`, from source
`f75ca3e9a9f0998aa375358ffb7b2dba13453a9e`. The only replacement is
`/opt/dxberry/lib/radio.sh`, root-owned mode 0644:

| Previous SHA-256 | Installed SHA-256 |
| --- | --- |
| `945efed162994ae7eb6c2d742d94621f6912d11d348cfcee4094f225ac52d562` | `b0eb3110558b7f3eacf107cd02ba99acc676c8684261d5769aedb46688bc683d` |

The exact previous library is retained at
`/root/dxb-radio-pin-identity-f75ca3e`; staging remains at
`/root/dxb-radio-pin-identity-stage`. The installation record is
`/var/lib/dxberry/local-patches/radio-pin-identity.json`. This supersedes the
radio-library hash in the publication-lock record, preserving the installed
publication gate and all other local patches.

The isolated candidate replaces only the pin lookup, kernel-name resolver and
original ALSA-name capture functions on the exact installed baseline. It adds
no development generation dependency, helper or epoch. All eight transfer
pieces and the 22,766-byte archive were verified (SHA-256
`1150814391b0857f6a4624b152ce4c1c30ee287ae387ac2dd0b596efc7483fa8`).
The installer pinned the old library/console, required no pending or active jobs,
retained existing operation gates, saved and fsynced the original, and used
atomic replacement with rollback on post-check failure.

Separate read-only diagnostics verified installed/original hashes and mode,
all seven unchanged service identities, the `.4` console, checked radio/TX/
helper/network files, Graywolf configuration and mixer. Both PCM streams stayed
RUNNING; installer HTTP and verified HTTPS version checks passed. The manual
Graywolf/CODEC setup remains unregistered. No radio command, registration,
record save/apply, update/settings/provisioning job, package upgrade or service
restart was performed by this library installation. Physical acceptance and
the broader handover protocol remain open.

The later [presence checkpoint maintenance](radio-wait-recheck.md) supersedes
this radio-library hash while preserving the matcher. It refreshes metadata at
named claim/cleanup boundaries; complete handover authority remains separate.
