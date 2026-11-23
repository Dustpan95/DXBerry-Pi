# Durable program generation foundation

Status: a development storage library, `station/internal/programstate`, and the
root `dxberry-program-state` helper below. The session template now invokes it
as its first activation condition. Complete writer coverage, daemon construction
and the production preset verifier remain open. This feature is not installed
on the Pi. The current Pi retains `.4` and the separately installed required-lock
patch.

A stopped/running snapshot can miss a complete start/stop cycle between reads.
Systemd can also garbage-collect an inactive unit, losing the old unit object's
history. The future managed-program protocol needs a generation that changes
before activation and survives those unit transitions. An unchanged cached
holder or current inactive unit state cannot supply that generation.

## Storage contract

`NewStore(stateDir)` accepts a canonical absolute server-configured path to an
trusted root-owned state directory. Construction performs no I/O; ordinary read
and advance require it to exist, while explicit preparation may create its final
component under a protected existing ancestor.
The fixed child is `program-state`; callers cannot choose filenames through
request data. The production owner is always root, including when construction
occurs under another UID.

| Operation | Behavior |
| --- | --- |
| `Read(ctx)` | Fresh epoch/path read only; missing, corrupt or untrusted read evidence is unavailable. Creates nothing. |
| `WithReadLease(ctx, callback)` | Root-only shared lock on existing valid metadata, held through a synchronous callback and fresh postcheck. Blocks cooperating advances; does not establish program-free state. |
| `PrepareParent(ctx)` | Root-only explicit final-directory preparation; validates existing paths without repair and does not initialize or rotate an epoch. |
| `Advance(ctx)` | Explicitly initializes or replaces the global generation under a process-shared lock. Refuses a damaged existing generation. |
| `WithAdvanceLease(ctx, callback)` | Advances first, then retains the exclusive lock through a trusted synchronous mutation and postcheck. Failures retain published generation and possible callback effects. |
| `Generation.Same(other)` | Compares two successful immutable receipts from the same store, including path/file identity and the private token. Performs no I/O and grants no permission. |

The child is mode 0700; `epoch` and `lock` are root-owned regular single-link
mode 0600 files. `epoch` contains exactly 64 lowercase hexadecimal characters
and a newline, representing a nonzero 32-byte cryptographic random token.
`lock` is empty. All path components are traversed by descriptor without
following symlinks. Root-owned sticky ancestors may contain a private fixture;
the configured parent must deny group/other writes. File and directory identity
are checked again through the configured path before publishing a read or write.
Receipts include metadata, so replacing the file with identical bytes changes
the receipt. JSON serialization exposes none of these private fields.
Ordinary `Read` does not acquire or validate the writer lock, or prove that the
writer path is operational. `Advance` validates the lock before writing.
The generation is global: there is no radio-name or unit selector in the API.

An advance takes a bounded exclusive `flock`, refuses invalid current state,
and obtains a fresh token. Zero or immediate repeated tokens are retried up to
four times. It exclusively creates a private temporary file, explicitly sets
0600 on that new descriptor, writes and fsyncs it, rechecks path/lock/current
generation, then renames and fsyncs the directory. The verified parent is synced
on mutations too. Success requires a fresh readback of the new generation.
Readers never adopt a temporary file. A killed writer can leave a private
temporary file; it is retained and ignored rather than reused as active state.

Failures before rename retain the previous active bytes, though an explicit
initialization can have created directories/lock files. Failure after rename
returns `ErrUncertain`: the token may already have changed. It is never silently
rolled back. A future start hook must refuse activation on either ordinary or
uncertain failure. Missing state can be initialized only by explicit `Advance`,
never by a reader guessing a default. Corrupt existing bytes are not repaired.

Storage phases use a two-second context limit with cancellation checkpoints and a
cancellable lock wait. This does not make individual local filesystem or entropy
operations preemptible. The helper below adds a process watchdog; callers still
need their own invocation deadline and termination handling.

## Shared read lease

`WithReadLease` opens the existing empty private lock read-only, validates it,
and acquires a shared `flock`. It creates or repairs nothing. After waiting,
it binds a fresh generation receipt to the held lock inode and configured
parent/child identities before invoking the callback. The callback receives
the caller's original context. The acquisition/preflight deadline does not
shorten a longer callback deadline.

The lock remains held until the callback and postcheck finish, including after
caller cancellation. Automatically unlocking a still-running callback would
allow a cooperating writer to enter while an operation that ignores
cancellation was still finishing. The trusted callback must finish or join all
its work before returning; it cannot retain an operation reservation through
an escaped receipt or background goroutine. It must not call `Advance` on the
same path while holding its shared lease, since that would contend with itself.

Postcheck uses a separate bounded context even if the caller was canceled.
Callback errors and caller cancellation are retained. Replaced or damaged
epoch, lock or directory evidence fails the postcheck with `ErrUncertain` and
the underlying refusal. This does not roll back or disprove callback effects.
The lock descriptor and token are not exposed through JSON or a helper command.
Non-Linux and unprivileged callers remain unavailable.

This primitive excludes cooperating generation advances only. A writer may
have advanced before acquisition and still be mutating. Cleanup can act without
advancing; privileged or nonparticipating writers can bypass the lock or replace
its path. Therefore it is not wired into preset authority. Mutation-phase writer
protection, installed-protocol/effective-profile binding and fresh pending-job,
process and restoration-journal checks remain required. Holding a blanket lock
while a parent waits for a unit hook or native IPC that needs the same lock can
deadlock; integration must establish phase ownership and lock order first.

Docker verification for this extension is recorded at
`/workspace/dxberry-program-lease-tests/`. Actual separate-process writer tests
verify refusal while the callback holds its lease and recovery afterward,
including cancellation while the callback is still running. Additional tests
cover concurrent shared readers, caller deadlines, canceled/exclusive-lock
acquisition, lock replacement while waiting, invalid/missing metadata,
postcallback replacement and callback errors. These are filesystem/process
fixtures, not systemd-manager, radio or hardware acceptance. No Pi installation
is part of this extension.

Full Go vet/race checks passed in Docker. A separate capability-free container
under UID 65534 passed the unprivileged refusal test. Native and ARM64 builds
produced all four executables with a local fixture version; ARM64 tests were
compiled but not executed. The non-Linux library also compiled. No release
artifact or hosted CI run is claimed.

## Mutation lease

`WithAdvanceLease` uses the same bounded publication path as ordinary `Advance`,
then retains its exclusive lock while running a trusted synchronous mutation
and the same fresh postcheck used by the read lease. The callback sees the new
generation and original caller context. It cannot run after a refused or
uncertain publication. A callback error or cancellation never restores the
previous generation. Even a failure before the callback can retain a token
already published; callers must not infer that no state changed from an error.

Separate-process readers and writers contend with this lease, and a held read
lease prevents publication and callback entry. Ordinary `Read` remains a
non-reserving metadata read. Cancellation does not unlock unfinished callback
work. A callback must finish or join all its work and must not call a generation
writer/read lease recursively or wait for child/IPC work that does so. No
descriptor, bypass flag or new helper opcode is exposed.

The [native mutation integration](native-mutation-leases.md) assigns the lease
to the local server/initialization owner. Clients release their entry stamp
before waiting for IPC. Release resume obtains external coordination outside
service/generation locks and rechecks its exact receipt inside the mutation.
Shell mutation phases and the complete installed-protocol/free-state verifier
remain open; this does not enable preset authority or modify the Pi.

## Required integration before authority

The stored generation is not evidence that tracking hooks are installed, that
all writers participate or that a program is currently stopped. It is not an
authorization lease, operation lock or lifecycle monitor. Root changes outside
the cooperative protocol can replace files or bypass tracking; this library
does not make that protocol mandatory by itself.

Before production use, the managed activation and ownership writers must rotate
the generation before any relevant work and propagate failure. The verifier
must independently bind the installed tracking protocol, exact current
registration/module classification and fresh unit/process state. It must detect
a change and return even after inactive-unit garbage collection. A later recall
also needs an operation boundary that excludes activation through the write;
a generation read followed by a later CAT write is insufficient.

Do not add the parent's record lock to session unit hooks: the CLI can already
hold it while waiting for those hooks. The future protocol must preserve that
lock ordering and keep emergency STOP/unkey available independently of tracking
storage failure. The activation condition below is implemented; complete writer
coverage and recall integration are not yet supplied.

The subsequent [radio mutation guards](radio-generation.md) now advance before
record publication, handover control work and coordinated apply/hotplug, with
explicit unconfirmed-release handling. They cover owner changes without an
activation; routing helpers outside apply, package writers and the complete
authority boundary remain open.

[Package mutation guards](package-generation.md) now use the shared common
advance protocol before installer, removal and update-job live work. They do
not supply whole-job exclusion or complete provisioning/native-helper coverage.

[Session/native acquisition guards](session-generation.md) now invalidate
before session start/preparation and native initialization/approval/start or
release maintenance. Cleanup remains independent of tracking.

## Root helper and packaging

The helper supports `dxberry-program-state [--state ABSOLUTE_DIRECTORY] advance`
and the explicit provisioning operation `prepare-advance`. Ordinary advance
requires its trusted parent to exist; provisioning may prepare only its final
directory under a trusted existing ancestor. The default is
`/var/lib/dxberry/station`. Only the explicit operator flag selects it. Environment
configuration and stdin are unused. No read/reset/repair, program start/stop,
radio selector or CAT operation exists. Duplicate/reordered/unknown arguments
are refused before store access. The browser CLI allowlist rejects this helper.

The [provisioning extension](provision-generation.md) describes parent creation,
failure semantics and checks. It supplies no repair/reset or program control.

Every normal response is JSON with `protocol: 1`. Success is exactly
`{"ok":true,"protocol":1}`; the token, paths and process information never appear
in the response. Failures have `ok: false` and a fixed `code`/`reason`: exit 2
for invalid options, 6 for privilege/storage refusal and 7 for uncertain outcome.
A changed generation whose response cannot be delivered also exits 7, without
restoring old bytes. A future hook must refuse both nonzero exits and malformed
or missing responses; success alone still does not establish program-free state.

The worker has a three-second context limit in addition to the store's limit.
If it does not return, the command reports uncertainty. The main process has a
3.25-second watchdog that requests exit 7 if a filesystem call or stdout pipe
stalls; that exit may have no JSON response. It cannot preempt uninterruptible
kernel I/O or guarantee scheduler timing. SIGPIPE is ignored so a closed
stdout pipe can return the intended uncertain exit instead of a signal exit.

`station/build.sh` builds this static helper beside the console, settings and
preset helpers for the selected architecture. Image/tree and update-archive
checks require its regular executable file; symlink placeholders are refused.
Image and update command links include it, and the existing rollback link cleanup
removes the dangling link when returning to a tree that predates it. CI/release
builds carry the helper and invoke the
[actual-process Docker check](../../tests/integration/README.program-state-cli.md).
The activation condition below uses the helper; no preset target verifier is
constructed.

## Session activation condition

The session template runs `+/opt/dxberry/bin/dxberry-program-state advance`
as its first `ExecCondition`, before the existing radio-presence condition,
preparation or main process. The leading `+` runs this command as root despite
the session's `dxbapps` user. There is no `-` error-ignore prefix. A helper
refusal or uncertainty prevents the remaining activation steps. As usual for
`ExecCondition` exits 1–254, this can skip activation without marking the unit
failed; the existing CLI start wait/error behavior is unchanged.

Using the first condition rather than `ExecStartPre` also records an ordinary
unplugged attempt before `present` skips the start. Successful helper execution
is required before preparation can change application settings or audio state.
Attempts canceled before the helper advances are not observed by this ledger;
this is not continuous observation of every manager state transition.

The radio tmpfiles rule creates `/var/lib/dxberry/station` as root:root 0700.
Radio setup installs and applies this rule before its first guarded apply;
applications setup runs later. This is independent of whether the console has
already created any settings. The rule creates only the trusted parent; it does
not initialize an epoch. `StateDirectory=` is deliberately absent from the
`dxbapps` session unit, so its user does not gain ownership of station state.
The source unit uses the default state path; custom daemon/helper paths still
need an explicitly matching verified tracking profile.

The prefix uses only the store's own lock. It does not acquire the parent's
record/apply/update locks or hold them while the parent waits for unit startup.
`ExecStopPost` remains the existing `dxberry-app stopped %i`, with no generation
dependency. The existing cleanup/unkey path remains independent of this store.

This covers the shared session activation entry point, not every ownership,
configuration or lifecycle writer. Effective unit properties/drop-ins, helper
identity, module classification, fresh unit/process evidence and the operation
exclusion boundary still need verification. Production `TargetSource` remains
unavailable; no generation or prefix success authorizes a radio write.

## Storage verification

Full Go vet and all-package race checks passed. Ten repeated package race runs
passed, including concurrent independent store instances, actual subprocess lock
contention/recovery, and a writer killed after temporary-file fsync but before
publication. The killed writer preserved the old active generation; the kernel
released its lock and a later advance succeeded while ignoring the orphan.

Other checks cover read-only absence, malformed and oversized bytes, path/leaf
symlinks, hard links, FIFO leaves, ownership/mode failures, cancellation, entropy
failure/collision, path/lock replacement, privacy and sync failure on either side
of publication. A restrictive-umask test first reproduced publication of an
invalid-mode epoch; explicitly setting the new temporary descriptor's private
mode fixes it before publication. The final checks passed. The decoder passed
414,225 fuzz cases. These are filesystem/process fixtures; a temporary file
standing in for unit state is not real systemd lifecycle acceptance.

Native and ARM64 test binaries compiled; ARM64 was not executed. The package
also compiled for Darwin, where operations are explicitly unavailable. No new
shell/browser/image or hardware acceptance is implied. Staticcheck and hosted CI
remain outstanding. Evidence is retained at
`/workspace/dxberry-program-generation-tests/`.

The compiled native tests also passed under UID 65534 with a read-only root,
no capabilities and no network. Constructor/refusal/decoder checks ran there;
root-positive filesystem cases explicitly skipped. DockerPC separately confirmed
the Pi's `.4` console, five active services and installed lock-patch script hashes,
and that no program-generation state directory exists there. This step performs
no Pi installation, service action or radio request.

## Helper verification

The helper/packaging milestone passed full Go vet/race, all 936 shell tests,
repository Bash syntax/ShellCheck and the tree check in Docker. Native and
ARM64 builds produced all four
executables. Both generation helpers are static ELF64 binaries for their
respective architectures; the ARM64 helper was not executed locally.

The actual native helper passed the root and UID 65534 process fixtures in
read-only, capability-free, network-free Docker containers. This includes real
lock contention/recovery, an inherited stdin pipe left open, a closed stdout
pipe after publication, and a full output pipe. The latter exited with
uncertainty through the actual main-process watchdog while retaining the newly
published generation. Refusal of corrupt active state preserved its bytes and
unrelated fixture state remained unchanged. The driver also refused existing
evidence rather than overwriting it. These checks establish helper process
behavior; the additional literal activation-prefix check is described below.

A separate explicitly non-deployable archive fixture retained the actual ARM64
helper's exact bytes, mode 0755 and root ownership. Its deliberately artificial
archive version differs from the console's development version; it is not a
release or image. Workflow YAML parsed locally and includes the actual-helper
check in the ARM64 CI/release jobs, but no hosted CI run is claimed.

Evidence is retained at `/workspace/dxberry-program-state-cli-tests/`, including
the native actual-process report, shell/Go logs, build outputs, ELF metadata and
archive checks. No helper is installed on the Pi by this step and no generation
directory is initialized there. Hook coverage, fresh program-free verification,
recall exclusion and physical/systemd acceptance remain open.

## Activation-condition verification

The activation follow-up passed all 938 shell tests, repository Bash syntax,
ShellCheck, the tree check and full Go vet/race checks in Docker. Native and
ARM64 builds produced all four executables. The actual native helper passed the
existing process checks plus execution of the literal first condition extracted
from the template. It succeeded while a parent held `/run/dxberry/record.lock`,
refused a contended store lock without changing the epoch, recovered after
release, and refused damaged epoch bytes. Template checks require generation
before presence/preparation, a non-ignored command, and unchanged main/cleanup
declarations. A separate shell fixture still unkeys through the existing stopped
handler with unusable generation state; its rig-control command is simulated.

Systemd 257.13 tools also ran inside capability-free, read-only Docker containers.
`systemd-analyze --generators=false verify` accepted the exact unit with its
declared executables present, without starting it. Actual `systemd-tmpfiles`
execution against a disposable root created root:root 0700 station state,
preserved unrelated data on a second run, and created no epoch directory.

A separate non-deployable archive fixture retained the actual ARM64 helper and
the exact unit/tmpfiles bytes. These checks are process, filesystem and offline
unit verification, not execution of the unit under a real systemd manager or
physical-radio acceptance. Complete writer coverage, effective-unit/profile
binding and recall exclusion remain open. No Pi file, unit or service is changed
by this follow-up. Evidence is retained at
`/workspace/dxberry-program-activation-tests/`.

The later [apply extension](radio-generation.md#apply-extension-evidence) moves
the same trusted-parent rule into the earlier radio setup step and verifies its
provisioning order and actual tmpfiles behavior there.
