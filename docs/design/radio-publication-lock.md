# Radio record publication exclusion

Status: implemented with an isolated Pi maintenance installation. Preset
authority remains unavailable.

Standalone apply holds apply 9 while reading the desired radio record and
deriving files/service state. Previously a radio change holding record 8 could
publish a new desired record during that work. The shared save function now
uses the same apply gate for its short publication phase.

## Scope and ordering

`dxb_radio_save` still canonicalizes and validates input first. Invalid input
returns 2 without acquiring a runtime lock or advancing generation. Valid input
enters a fixed private publication callback under apply 9. Development source
then advances generation, replaces the record, checks readback and updates the
in-memory record while retaining that gate. Acquisition failure returns 6 before
generation, record or cache changes. A later write failure retains any published
generation; no earlier receipt is restored.

The existing full/hotplug apply wrapper uses the same private acquisition
helper with its existing coordinator callback. Bounded wait, failure codes and
descriptor-close behavior remain unchanged. The callbacks are fixed trusted
function names in code; command/JSON input cannot select them. There is no
inherited-descriptor or caller-controlled bypass. Callbacks run synchronously
in the same shell, preserving the established cache update behavior.

Ordinary CLI writers retain record 8 outside save, so their publication order
is record 8, apply 9, then the development generation advance. Standalone apply
takes apply 9 before its own development advance. Direct save callers still
require their existing record serialization; a validated payload computed from
an obsolete record is not made fresh by waiting for this gate.

The publication callback performs no scanner, service start/stop, manager wait,
native IPC or radio control. It closes its apply descriptor before returning to
the handover caller, which can then start/wait for services. Claim/release do
not retain apply 9 for their full lifetime. The prerequisite-ordering regression
from the [mutation-phase audit](radio-mutation-phases.md) remains required.

This closes publication during a cooperating apply. It does not serialize the
whole handover: old-owner cleanup, later routing/wiring, card restoration and
direct component calls still need phase boundaries. Unit hooks keep their
existing parent-lock independence. Emergency STOP/unkey remains separate.
Pending intent, hotplug reconciliation, post-wait validation and the complete
installed-protocol/free-state verifier remain open. No production preset target
is constructed by this change.

## Verification

Docker command/filesystem fixtures check contended save refusal and retry,
unchanged record/cache on refusal, replacement and cache update under real
flock exclusion, failed replacement releasing its gate, unavailable lock
support, bad lock/run-directory paths and validation before locking. All four
new groups fail on the previous save function and pass on the new implementation.
The existing session-ordering regression also passes.

The generation-record unit fixture now supplies its own temporary runtime
directory. The actual-helper integration fixture additionally holds apply 9
from a separate process and verifies refusal without a real generation, record
or cache change; release permits the ownership-change retry. Those runs remain
hardware-free, with simulated services and private tmpfs state. They do not
establish real manager, device, RF, audio or first-boot acceptance. Evidence is
retained at `/workspace/dxberry-radio-publication-tests/`.

All six actual-helper containers passed: root CLI, unprivileged refusal,
activation prefix, record writer, apply coordinator and provisioning bootstrap.
The unchanged native helper has SHA-256
`e2900b447bc0f15012ef75872f6df74938f6490393956bd31c6f1f670eec9161`.
The extended tagged Go fixture compiled and passed targeted vet in Docker.
No production Go source changed or binary rebuild/deployment was required.

All 995 development shell tests and 906 isolated maintenance shell tests passed
in Docker with zero failures, together with both Bash syntax/ShellCheck and tree
checks. Installer fixtures verified atomic replacement, complete rollback and
retained diagnostics if restoration is incomplete. Full Go and browser suites
were not repeated for the shell runtime/test-only Go changes.

## Pi maintenance installation

Installed through DockerPC at `2026-11-23T13:35:53Z`, from source
`1d55382586fe802545e183bc48a9297280826195`. The isolated library starts from
the exact installed baseline and replaces only the validated save/publication
and apply-wrapper blocks. Its only difference from the tested development
blocks is omission of the development-only generation call. No generation
helper or epoch directory is installed or required by this maintenance patch.

| Item | SHA-256 |
| --- | --- |
| Installed `/opt/dxberry/lib/radio.sh` | `945efed162994ae7eb6c2d742d94621f6912d11d348cfcee4094f225ac52d562` |
| Saved original library | `781a8ed111c423a3f266d38151d2b54a1b08df8ce45603a3c9642760dc4bb654` |
| 22,567-byte maintenance archive | `58fd732bd26ade1d226055fc35fd6cdd65660f35bab4245c2c8a863f9e83d01e` |

The library remains root-owned mode 0644. Its original and installation checks
are retained at `/root/dxb-radio-publication-1d55382`; staging remains at
`/root/dxb-radio-publication-stage`. The record is
`/var/lib/dxberry/local-patches/radio-publication-lock.json`. This supersedes
only the installed library hash in the earlier radio-locks record.

All six upload pieces, the archive and staged source/modes were verified.
Installation acquired update/settings/provisioning execution gates, then the
existing admission/record/apply locks. It pinned the old library and `.4`
console, refused active/pending jobs or provisioners, synchronized the original
and atomically replaced the library with verified rollback on failure.

Independent read-only diagnostics verified installed/backup hashes, unchanged
identities of all five services, previous patch records, checked radio/TX/helper/
network files, Graywolf configuration tables and mixer. Both PCM streams stayed
RUNNING; HTTP and verified HTTPS version checks passed. No radio was registered,
no record save/apply or job/provisioning run was performed, and no service was
restarted. Real manager and hardware acceptance remain open.

The later [pin metadata maintenance](radio-pin-identity.md) supersedes this
radio-library hash while preserving the publication gate and its ordering.
