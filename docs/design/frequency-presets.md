# Named frequency presets: first design

Status: the development definition parser, store, configuration CLI, build
packaging, authenticated CLI transport and injected read-only preview service
and authenticated preview HTTP adapter are implemented. The production target
verifier, recall/undo and interface remain open. Nothing from this feature is installed
on the Pi. The preview service connects fresh rig/registration reads and
continuous tab-claim evidence, with no cached-evidence fallback. This develops
the carried-over presets/profiles idea independently of native-program packages.

## First useful scope

Start with named frequency presets for one registered radio. Save, rename,
delete and preview definitions without tuning anything. A later guarded recall
moves only the currently selected receive VFO's frequency. It does not change
mode, filter, VFO selection, split, power, audio devices, program ownership,
PTT method or the station's transmit policy.

A full VFO profile is a later extension. In particular, calling the existing
frequency intent and then the mode intent is not an atomic profile operation.
Hamlib can accept the first command and reject or time out on the second. The
initial format therefore has no mode/passband or arbitrary command field.
Unknown future fields are rejected rather than partially applied.

The current manual Graywolf/CODEC setup remains outside this feature. An empty
registered-radio inventory produces no preset target; implementation or
acceptance must not register that CODEC as a convenient test setup.

## Existing seams and missing guarantees

| Existing component | What it establishes | Additional work needed |
| --- | --- | --- |
| `rig.Intents.Freq` | Frequency must fall within discovered receive ranges; uses the current writer and informs the SWR guard after success | A preset-specific ownership and receive-only operation boundary |
| `rig.Poller.Write` | Writes are serialized and optimistic cache changes are reverted when the callback fails | Actual readback and a result that distinguishes confirmed, failed and unknown hardware state |
| `rig.Poller.Observe` | Direct receive/VFO/frequency reads with local generation and age checks | Fresh registration/program/owner evidence, preview tokens and recall revalidation |
| `session.Owners` | Tab/program claims, transmit-aware takeover and private continuous tab-claim generations | Fresh program evidence and holding the relevant identity/TX exclusion across recall, rather than checking a snapshot before a later write |
| `radios.Watcher` and `RegistrationReader` | Last-good inventory for existing pages, plus separate fresh trusted registration evidence | Bind fresh registration to the current poller/program evidence; cached inventory alone cannot authorize recall |
| Preset configuration CLI | Authenticated strict metadata requests use the trusted station state path | Preset editor interface and separate preview/recall routes |

`State.UpdatedAt` and absence of an optimistic flag do not establish that every
field was recently read. Capture-from-radio and recall preview need their own
fresh frequency/VFO/PTT observation. Model capabilities currently expose
frequency ranges and mode names, not a complete per-mode filter-width contract;
the design must not invent that validation for later profiles.

## Definition format and persistence

Storage is a separate root-owned `presets` directory under station state,
one document per validated radio name. Do not put presets into the guard-settings
file or any program's profile/configuration. Definitions are station settings;
apply receipts are separate operational state.

| Field | Version 1 meaning |
| --- | --- |
| `version` | Integer `1`; unknown versions are unavailable |
| `radio` | Registered radio name; not a path or physical-hardware identity claim |
| `revision` | Opaque 64-character lowercase hexadecimal revision, replaced on every edit |
| `presets` | Ordered list of at most 64 definitions |
| Definition `id` | Stable random 32-character lowercase hexadecimal identifier |
| Definition `label` | Nonblank UTF-8 plain text, at most 80 Unicode code points/320 bytes, no control characters |
| Definition `frequency_hz` | Positive integer Hz, at most the JavaScript safe-integer maximum |

Labels are display text, never filenames, shell fragments or identifiers.
Frequency range compatibility is established against the selected radio during
preview; editing a saved definition never implies that tuning it is supported.
Storage can prepare or retain definitions for a valid radio-name namespace when
that radio is absent. It creates no registration or recall target. Actual preview
will require the fresh registered-radio evidence described below.

Reads are bounded to 128 KiB and reject duplicate/unknown keys, wrong types,
nulls, invalid UTF-8, duplicate IDs and trailing JSON. Missing files mean an empty
new library; an unreadable or damaged existing file is unavailable and must not
be overwritten as if empty. The state parent must already exist, have the expected
owner and not be group/world writable. Preset directories are private mode 0700;
documents are ordinary single-link mode 0600 files. Parent/directory leaves and
documents refuse symlinks. Child access uses verified directory descriptors.
Failures before publication preserve previous active bytes. A failure after
rename, including directory fsync, readback or response delivery, can leave a new
revision present: report an uncertain outcome and reload rather than blindly
reverting or assuming that nothing changed.

The development `dxberry-preset` configuration command accepts structured JSON
on stdin and returns JSON. It provides list and compare-and-swap edit operations;
it never talks to rigctld, starts a program or tunes a radio. Every edit supplies
the revision it read, or an explicit absent-file precondition for first creation.
A process-shared lock, same-directory temporary file, file fsync, atomic rename
and directory fsync protect writes. Read back the new revision before reporting
success. A conflicting revision returns conflict and leaves the file unchanged.
Sync the parent directory too, so a newly created preset directory is included
in the durability checks. Locks are cancellable and bounded to two seconds;
the CLI stops waiting for stalled stdin after five seconds and exits. Interrupted writers can leave
private temporary files; those files are never read as active documents or
silently reused. Deleting the last definition retains an empty revisioned file.
Radio removal retains definitions for explicit later management; name reuse
does not grant an automatic recall or silently rebind an old preview.

## Development CLI contract

The command requires root and defaults to
`/var/lib/dxberry/station/presets/<radio>.json`. Its trusted operator flag
`--state` selects an existing station-state directory for isolated development;
request JSON cannot select paths. `--json` is accepted and all responses are
JSON. No subprocess, rigctld connection or application-service call is made.

| Operation | Exact request fields in addition to `version: 1`, `operation`, `radio` |
| --- | --- |
| `list` | None; missing library returns revision `absent` and an empty array without creating state |
| `create` | `expected_revision`, `label`, `frequency_hz`; server generates ID and new revision |
| `update` | `expected_revision`, `id`, `label`, `frequency_hz`; preserves ID and list position |
| `delete` | `expected_revision`, `id`; preserves remaining IDs and order |

Use `expected_revision: "absent"` only to create the first definition. Later
edits must use the revision returned by the latest successful list/edit. Renaming
uses `update` with the current frequency included. Null, misspelled, duplicate,
extra or operation-inappropriate fields are refused; no arbitrary settings or
command field is accepted.

Success returns `ok: true` and `library`. Failure returns `ok: false`, a fixed
`code`/`reason` and no library. Exit codes are 2 invalid, 3 missing preset,
4 revision conflict, 5 preset limit, 6 unavailable/privilege failure and
7 uncertain mutation outcome. A lost response never proves that an edit did
not happen; reload instead of retrying with an absent/stale precondition.

Build and exercise this development command inside a disposable Linux Docker
container, from `station/`, with no station configuration or hardware mounted:

```sh
CGO_ENABLED=0 go build -trimpath -o /tmp/dxberry-preset ./cmd/dxberry-preset
fixture_state=$(mktemp -d)
printf '%s\n' '{"version":1,"operation":"create","radio":"radio1","expected_revision":"absent","label":"20 m receive","frequency_hz":14074000}' |
  /tmp/dxberry-preset --json --state "$fixture_state"
```

`station/build.sh` now builds the preset executable alongside the console and
application-user settings helper. CI/release artifact paths and image/update
inputs include it; missing or non-executable helpers prevent packaging. A failed
console or settings-helper build cannot be hidden by a later successful build.
Fresh images add the normal command symlink; existing update/rollback symlink
handling already adds/removes command links for the installed tree. Preset data
is retained as station state and is not replayed by an update or rollback.
No hosted release job, public artifact, flashable image or Pi installation has
been performed for this feature.

## Authenticated configuration transport

The development daemon accepts preset definitions through its existing
`POST /api/cli/dxberry-preset` transport. The outer body has exactly `args` and
`stdin`; `args` must be `["--json"]`, and `stdin` is the command's request JSON
as a string. The outer body is bounded to 128 KiB. Both layers refuse malformed
UTF-8/surrogate escapes, ambiguous or extra fields and trailing JSON before a
helper starts. A request cannot supply an executable, state path or other flag.

`CLI.New` leaves this command disabled until trusted server code calls
`SetPresetState` with an absolute normalized state path. The daemon uses its
configured `-state` value, resolved to an absolute path. The runner adds that
path as a private helper argument; it does not fall back to another station's
default state when a custom state path was configured. Startup performs no
preset directory creation or helper invocation.

Normal station session and same-origin middleware protect this transport.
Responses are uncached. Envelope/schema/argument refusals return HTTP 400
before process execution. A helper that runs returns the existing CLI envelope
with `exit`, `stdout` and `stderr`; callers must inspect its exit and the JSON
inside stdout, rather than treating HTTP 200 as edit success. Revision conflicts
therefore retain CLI exit 4, with no previous library presented as success.

Logs retain only the command/validated public argument shape, not stdin or the
server's state path. Definition reads and edits do not request a full-station
report: that report contains no preset data, and the command response supplies
the current library revision. No background preset polling is introduced.

## Storage software evidence

On November 23, Docker Go vet and race checks passed for the full development
tree and the focused command/store packages. Coverage includes strict decoding,
64-definition bounds, revision conflicts, eight competing store handles, three
competing processes, cancellation while another descriptor holds the shared lock, and a
writer killed after syncing its temporary file but before publication.
Filesystem checks cover damaged/oversized documents, wrong identity or
permissions, hardlinks, symlinks, FIFO/directory substitution and replacement
outside the lock protocol. Injected file/parent fsync and rename failures retain
old active bytes; failures after publication return uncertain with no library.
These are software failure checks, not a physical power-loss exercise.

The parser also passed 258,406 fuzz executions. The compiled native command
passed eight [isolated CLI integration checks](../../tests/integration/README.preset-cli.md),
including actual root/nonroot execution and an inherited OS stdin pipe left
open. This caught an issue an interruptible in-memory pipe did not: closing
`os.Stdin` could leave the read blocked. The single-purpose CLI now bounds its
wait independently of interruptible close and exits at the deadline. Existing
evidence was also refused and preserved on a second test invocation.

Native Linux and ARM64 command builds passed in Docker with CGO disabled and
VCS stamping disabled for the isolated repository mount. The ARM64 ELF machine
is AArch64; its SHA-256 is
`da4474159dc27d600df28d0f12c70a71eb93a94ad6107386d7587b5576c09e07`.
It was not executed or installed on the Pi. Evidence is retained in
`/workspace/dxberry-preset-store-tests/`. Staticcheck remains unavailable in the
cached environment; this is not a complete release-check or radio acceptance claim.

## Transport and package evidence

The subsequent development tree passed Docker Go vet/race checks, and the full
shell suite passed 922 tests with zero failures. Bash syntax, ShellCheck and
workflow YAML parsing passed. Transport tests cover disabled/untrusted server
configuration, the exact helper path/arguments/environment, both JSON layers,
authentication/same-origin refusal and private-input logging. A fuzz regression
retains empty-array representation rather than turning it into null; the final
bounded-wrapper fuzz run passed. Unknown/control requests remain refused.

The [actual-helper HTTP fixture](../../tests/integration/README.preset-cli.md)
passed using a root-owned empty private tmpfs, production middleware/runner and
the compiled helper. It exercised list/create/conflict/update/delete, refused
requests before execution, retained unrelated state and observed zero full-report
refresh requests. Its authentication source is synthetic, not a PAM-login test.
Existing fixture evidence was refused and preserved on repeat invocation.

Native and ARM64 builds of all three executables passed through `station/build.sh`.
The integrated ARM64 helper SHA-256 is
`8bc3096385ee67eb19c564d6a5cdcb0a1843e12d8089ea6ddc7199e7a8baa7b1`.
An explicitly non-deployable local archive fixture retained the real helper's
exact bytes and mode 0755/root ownership; the real-image first-boot gate remains
open. Evidence is retained at `/workspace/dxberry-preset-integration-tests/`.
CI/release jobs are configured to run the Docker HTTP fixture against their
built helper; locally parsed workflow configuration is not a hosted CI pass.
The Pi remains on its separate `.4` maintenance console without this feature.

## Fresh registration software foundation

Tracing the production verifier also exposed a [rigctld pin-binding and retry
problem](rigctld-binding.md). The development fix makes used-pin changes affect
the desired environment and retains rejected restart intent. It is not proof
that a live process has applied the desired configuration, and does not enable
the preview backend by itself.

`radios.NewRegistrationReader` prepares a lazy read-only source for the trusted
server-configured radio-record path. It expects root ownership in production.
Each `Read` traverses directories by descriptor, refuses symlinks and writable
untrusted ancestors/parents, and opens the leaf without blocking on a FIFO.
Root-owned sticky ancestors may contain a private fixture directory; the
immediate parent must deny group/other writes. The leaf must be a single-link
regular file owned by the expected owner, with no group/other write access.

The file is bounded to 2 MiB and 128 radios. The reader rejects malformed JSON,
duplicate keys at any depth, malformed Unicode, excessive nesting, unsupported
version, null or mistyped critical fields, ambiguous case aliases and duplicate
rigctld ports. It checks the fields needed for radio identity and routing;
this does not replace the provisioning command's full profile validation.
Unknown extension metadata is not interpreted but remains part of the identity
digest. An omitted historical `rig.control` retains the watcher's empty value
(the manager already treats it as Hamlib); explicit control values are strict.

File metadata must agree before/after the read, and reopening through the
trusted path must still find that file and parent. The immutable registration
holds the full-file digest and private file/parent identity. `Same` compares
two successful reads from the same reader; exact-byte atomic replacement,
metadata changes, directory replacement or changes elsewhere in the document
invalidate the previous identity conservatively. No private selector details
or digest are added to a wire response. Cancellation is checked before work
and before returning evidence; this is local-file I/O, not a hard preemptible
filesystem deadline.

The watcher keeps its existing last-good behavior. The separate reader reports
missing, malformed or untrusted current data without falling back to the cache,
creating a registration or touching the radio. Empty inventory means no target.
Fresh configuration identity does not prove physical USB presence, rigctld's
effective configuration or the attached radio's identity. Production preview
still needs to bind these sources; no production route uses this reader yet.

Docker Go vet and the full race-test suite passed. Parser fuzzing passed
322,200 executions. Filesystem fixtures cover symlinks, wrong ownership and
writable permissions, hard links, FIFO/directory leaves, missing/oversized data,
stale watcher evidence and identity changes. The compiled radio test suite also
passed under UID 65534 in a Docker container with a read-only root and private
temporary files. Its internal fixture ownership follows the test UID; the
public constructor's root-only ownership policy is checked separately and is
not relaxed. Native and ARM64 builds passed, including
ARM64 compilation of the radio test executable; no ARM64 execution or physical
acceptance is claimed. Evidence is retained at
`/workspace/dxberry-registration-tests/`. Staticcheck remains unavailable.

## Managed connection software foundation

`rig.Client.ConnectionEndpoint` exposes immutable private metadata captured from
the existing Linux TCP descriptor before its connected callback: local/peer
addresses, local socket inode/device/UID and connection generation. The UID is
the local socket's owner, not the peer process's UID. Capture uses read-only
`fstat` through `SyscallConn.Control`; it duplicates no descriptor, sends no
command and changes no socket settings. Unknown metadata leaves ordinary
protocol operation available but cannot qualify a preview target. Other
platforms report metadata unavailable.

`rig.Manager.ConnectionSnapshot` binds that socket to one existing manager
entry, reduced radio record and poller under the manager mutex. It refuses a
peer outside the configured `127.0.0.1` rigctld port. Its local `Current` check
refuses changed records, replaced/removed entries, canceled clients and changed
connections. Metadata lookups do not dial, publish events or start services;
private evidence is not included in radio state or JSON.

This is a connection anchor, not a production target verifier. A peer can stop
responding before the client discovers it. Fresh kernel connection state,
reverse accepted socket, service process/invocation, executable, cgroup,
effective configuration and opened-device identity still need verification,
alongside authoritative program-free evidence and fresh registration. Neither
the desired pin fingerprint nor this snapshot proves physical radio identity.
The preview HTTP backend remains unavailable until those checks are supplied.

Real TCP fixtures checked the captured descriptor against `/proc/self/fd` and
the established kernel TCP tuple, reconnection invalidation, callback ordering,
unknown metadata without protocol failure, changed manager records and wrong
peer refusal. These checks also passed in a Docker container under UID 65534
with a read-only root and no capabilities. The first nonroot run encountered
an unreadable mounted fixture; a separate readable copy corrected the fixture
without relaxing production checks.

The full race run exposed an existing poller initialization race between its
newly registered state callback and constructor's connected-state assignment.
That assignment now takes the same poller mutex as the callback. After the fix,
25 focused race-test runs and the full Go vet/race suite passed in Docker.
Native and ARM64 builds of all three executables and ARM64 rig-test compilation
passed. Evidence, including the original race report, is retained at
`/workspace/dxberry-connection-binding-tests/`. These are software/kernel
fixtures, not real rigctld, systemd, USB or RF acceptance. Nothing was installed
on the Pi, and staticcheck remains unavailable.

## Peer process and socket evidence

`Endpoint.InspectPeer(ctx, expectedPID)` now supplies the next Linux-only
read-only check. The expected PID must come from a separate trusted service
inspection. The method requires the exact IPv4 loopback listener and both
directions of the established TCP connection. The local row must match the
captured socket inode and UID; the reverse row and listener must match the
expected process UID. That process must actually hold descriptors for both
the listener and accepted socket. Duplicate, wildcard or additional IPv6
listeners on that port are refused, as are unknown or incomplete tables.

Reads are anchored to directory descriptors under a verified procfs mount.
The peer must share the client's current network namespace, have a live
process start identity and consistent real/effective/saved/filesystem UIDs.
Two table/descriptor passes and repeated process snapshots must agree.
Executable metadata, command-line bytes and cgroup bytes participate in the
private identity comparison without being returned to the browser. The
immutable `Peer.Same` compares two successful inspections; callers must obtain
a new inspection rather than treating it as a live check or extending a token.

The check reads bounded local metadata only: at most 2 MiB/8,192 rows per TCP
table, 1,024 descriptors and 64 KiB per process file. It honors cancellation
between steps and limits its overall context to two seconds. Kernel filesystem
reads themselves are not hard preemptible. No descriptor is duplicated, no
network connection or protocol command is sent, and no unit, process or radio
setting is changed. Unsupported platforms return unavailable.

This binds a process to a connection, without declaring that process trusted.
The production verifier still needs unit/invocation, trusted executable,
effective arguments/configuration, actual opened-device/USB identity and
authoritative program-free evidence. These sequential metadata snapshots are
not an atomic operation boundary or proof of physical radio behavior. The
production preview backend remains disabled and none of this work is installed
on the Pi.

Docker tests used separate real processes and kernel sockets. They refused a
different expected PID, removed listener, half-closed connection and an accepted
socket transferred to another process while the original listener and exact
established tuple stayed present. Malformed/ambiguous table and process identity
fixtures also passed. The full Go vet/race suite, 337,778 parser fuzz cases and
the compiled checks under UID 65534 with a read-only root and no capabilities
passed. Native/ARM64 builds and ARM64 test compilation passed; ARM64 execution
and real rigctld/systemd/USB/RF acceptance remain open. Evidence is retained at
`/workspace/dxberry-peer-process-tests/`. Staticcheck remains unavailable.

## Service identity software foundation

The lazy `rig.NewServiceReader` now inspects `rigctld@<radio>.service` for a
separately selected current endpoint. The production paths are fixed to
`/proc`, `/sys/fs/cgroup` and `/usr/bin/rigctld`; neither request data nor a
public constructor can override paths, commands or identity sources. Reading
requires root and performs only `systemctl show` plus local metadata reads.
The constructor does no I/O and is not wired into the daemon's preview backend.

The exact unit ID must be loaded, active and running with a positive main PID,
nonzero invocation ID and a normalized service cgroup below `/system.slice`.
The main PID must own the exact listener and accepted socket established by
`InspectPeer`. Its process fingerprint, start identity, root UID, network
namespace and executable must agree with that fresh peer evidence. Executable
and directory checks refuse symlinks, untrusted ownership, writable paths,
hardlinked executable leaves and non-executable files. The cgroup must be a
v2 domain, list the main PID and match the process's actual cgroup path.

Unit, peer, trusted executable and process/cgroup evidence are read again
before returning success. Identity changes or missing evidence return no
previous observation. The private immutable `ServiceObservation.Same` compares
two successful reads without performing I/O or extending their lifetime.
The overall context is bounded to two seconds; local filesystem reads remain
non-preemptible. Metadata subprocesses have a clean environment, bounded
stdout, discarded stderr and cancellation of their own process group. No
service start/restart, radio command or settings write occurs.

This establishes service/process identity, without interpreting effective
rig arguments or proving that opened USB/device descriptors match fresh
registration. Those checks, authoritative program-free evidence and the
complete production target source remain open. Sequential observations do not
make a later operation atomic. The preview backend stays disabled and this
development code is not installed on the Pi.

Docker root-owned filesystem fixtures with synthetic unit and peer metadata
cover successful binding, changed invocation/PID/executable/cgroup, unknown
states, untrusted paths, FIFO substitution, cancellation and stale-result
refusal. They are not a real systemd service acceptance test; the separate peer
tests establish actual kernel socket ownership. Ten focused race runs, the
full Go vet/race suite and 280,821 service-metadata fuzz cases passed. A compiled
nonroot run verified refusal before manager queries and metadata subprocess
limits/cancellation; root-only positive fixtures are skipped there. Native and
ARM64 builds and ARM64 test compilation passed without execution on the Pi.
Evidence is retained at `/workspace/dxberry-service-binding-tests/`.
Staticcheck and actual systemd/device/application acceptance remain open.

## Registration-to-command software foundation

`Registration.Configuration` now returns a separate immutable control view
when all required routing fields validate. A legacy or incomplete record can
still supply its existing reduced radio/display identity while this additional
proof is unavailable. The watcher and its last-good behavior are unchanged.
The control view is parsed before the final trusted-path and cancellation
checks, so the additional work does not bypass those read-side checks.

The view requires explicit CAT, serial-PTT and HID entries (possibly null),
positive model and nonnegative baud/GPIO integers in the supported 32-bit range,
known control/PTT values and bounded USB selectors. Unknown USB ID or serial
metadata stays unknown; a known empty serial is distinct. All selector fields
remain private and serialize as an empty JSON object. Missing fields, aliases,
invalid types and malformed selectors cannot produce a command proof.

`RigConfiguration.Rigctld(ownerKind)` describes the existing provisioning
argument vector and only the CAT/serial/HID pins it references. It handles
native and flrig control, separate serial PTT, session-only CM108/GPIO routing,
unset pins and the historical Hamlib control default. Owner kind must come
from fresh trusted program-module classification, not a browser field or an
assumption that a stopped program is unassigned. The view neither loads a
module nor establishes program-free state. It returns data, never executes it.

Fresh peer inspection also captures a private digest of the actual procfs
NUL-separated argument bytes. `ServiceObservation.MatchesCommand` requires the
same radio name, actual peer port and exact argument digest. Extra, duplicated,
missing or different options are refused rather than normalized into a match.
This pure comparison adds no freshness by itself: callers must obtain fresh
registration/service reads and retain their identity checks. USB metadata
changes can leave the argument vector unchanged, so matching arguments still
do not prove which device descriptors the process actually holds.

A separate [native USB device reader](rig-device-binding.md) now checks the
current pinned node against the running process's descriptor and USB metadata.
It remains a development-only foundation with synthetic whole-reader fixtures
and separate real kernel descriptor checks. Flrig/GPIO support, authoritative
program-free evidence, the complete production target source and actual device
acceptance remain open. Dummy/default-device configurations are not thereby
established as physical frequency targets. No preview backend, interface or
recall is enabled, no service is started and nothing is installed on the Pi.

Docker Go vet/race checks and ten focused command/peer race runs passed.
Configuration fuzzing passed 208,895 cases. A nonroot, read-only Docker run
compared 13 routing configurations across all three owner kinds against the
unchanged Bash provisioning function: all 39 argument vectors and used-pin sets
matched. That fixture runs only the environment-derivation function, with a
no-op warning sink; it starts no service. Malformed-control, unknown-pin-identity
and actual child-process argument-digest checks also passed. Service identity
fixtures remain synthetic; the nonroot run skips their root-only positives.
Native/ARM64 builds, ARM64 test compilation and a Darwin package compile check
passed. No ARM64/Darwin execution or physical acceptance is claimed. Evidence
is retained at `/workspace/dxberry-rig-command-tests/`; staticcheck remains open.

## Fresh observation software foundation

`rig.Poller.Observe` now performs an on-demand read sequence: PTT, VFO,
frequency, frequency, VFO, PTT. Both frequencies and VFOs must agree, and both
PTT readings must explicitly report receive. Unknown/malformed values, missing
PTT/VFO capabilities and relative or memory selectors are refused. It ignores
last-good and optimistic display values and does not update or publish that
display cache. It emits only `get_` commands, claims no owner and starts no
application. No production route calls it yet.

The immutable result is tied to its poller, write generation, capability/link/
station-key transition epoch and synchronous TCP connection generation. The
client's connection-generation accessor does not wait for asynchronous link
notifications. Writes, including failed writes, reconnections, replaced
capabilities and station key-on/key-off invalidate the evidence. A replacement
poller cannot accept another poller's result. Observation admission allows one
sequence per poller; a concurrent request returns busy rather than queueing six
more reads. The sequence respects the caller's shorter cancellation/deadline
and has a two-second overall limit. Evidence expires two seconds from the
sequence's start, using the clock's monotonic component when available.

This code never holds the poller's write mutex. An already executing protocol
read can still delay a write on the shared FIFO client until that command ends
or times out; it is not hard preemption or an urgent-STOP acceptance claim.
`ObservationCurrent` checks local generation and age only. It cannot detect a
silent front-panel change. Separate reads cannot rule out a front-panel change
and return between commands, and successful CAT reads are not proof of physical
PTT behavior. The complete preview still needs fresh trusted registration,
target range, program/owner evidence, tab binding and expiration/token handling.
Recall still needs fresh readback and its shared operation boundary.

Docker Go vet and the full race-test suite passed. Focused checks cover stale
and optimistic display caches, unknown capabilities, malformed/duplicate
values, transmit state, changing readings, connection/capability/write changes,
age and cancellation. A blocked observation did not hold the write mutex, and
a concurrent observation was refused. A real loopback TCP fixture exercised
the extended protocol's six read commands; a stalled reply respected a shorter
caller deadline without retry. A separate queued-event check confirmed that
the client connection generation advances before callback delivery. Native and
ARM64 builds passed; no ARM64 execution, radio test or Pi installation occurred.
Evidence is retained at `/workspace/dxberry-observation-tests/`. Staticcheck
remains unavailable; these checks do not close the complete release gates.

## Continuous tab-claim identity

`session.Owners` now assigns a private process-local generation to every new
continuous claim. A same-holder renewal, display-label change or ordinary
`Touch` preserves that generation. Release/reclaim, expiry, takeover to another
holder and return, or a holder-kind change replace it. Late `Touch` after idle
expiry or a backward clock step renews with a new generation, so it cannot
revive evidence from an earlier continuous claim. Allocation is serialized
under the existing owner mutex and never wraps; exhaustion refuses a new
identity without replacing a live holder.

`ObserveTabClaim` and `TabClaimCurrent` read only the tab-claim record. They do
not acquire, renew, expire or release ownership, run `OnDrop`, probe programs,
check PTT, or keep an operation lock across later work. An expired or negative-age
claim is unavailable to this observation, while normal owner operations retain
their existing expiry/drop behavior. Evidence is tied to the `Owners` instance,
radio, login/tab ID and continuous generation; no session ID or generation is
added to an event or persistent record.

This local observation is not the complete effective owner: a running program
may take priority over a stored tab claim. Preview must independently verify
fresh program state and radio/PTT evidence and enforce its own short lifetime.
Claim identity does not make a later check-then-write atomic or replace the
shared recall/key-on/STOP boundary.

Docker checks cover renewal versus release/reclaim, takeover-and-return,
expiry, last-socket close, wrong/foreign identities, read-side callback/idle
preservation, counter exhaustion and concurrent claim generation. A targeted
regression first reproduced late `Touch` reviving old evidence, then passed
after discontinuous renewal received a new generation. The full Go vet/race
suite passed. An existing manager test's event-order assumption surfaced in
the first full run; its fixture now forces capabilities to arrive before the
first frequency and waits for the subsequent frequency update. That test
passed 25 race-test runs without changing production event behavior.
Native and ARM64 builds passed, including ARM64 session-test compilation.
Evidence is retained at `/workspace/dxberry-claim-observation-tests/`.
No ARM64 execution, physical acceptance or Pi installation occurred.

## Injected preview service

`presetpreview.Service` loads the saved preset by registered radio name, stable
ID and exact library revision. Its input has no frequency, executable, path or
radio selector override. Trusted server code supplies the authenticated login
and tab separately. `Preview` returns only the selected public definition,
observed VFO/current frequency, requested frequency, difference and an opaque
token with its remaining lifetime. No HTTP route is registered by this package.

Construction requires the real definition store, fresh registration reader,
owners and an explicit `TargetSource`. A missing or uninitialized service is
unavailable. The target source must freshly verify the exact registration's
effective route/poller and authoritative program-free state every time. Its
private identity must change after route or program ownership lifecycle changes,
including a change and return. It must return immutable capability snapshots
and respect cancellation. Cached inventory, reduced `Radio` equality, cached
program holders or a guessed absence of a program cannot implement this contract.
No production verifier or permissive fallback is supplied yet.

The separate [radio lock guards](radio-locks.md) remove the existing shell
writers' fallback when locking is unavailable. They do not add lifecycle
identity or serialize the unit hooks, and do not satisfy `TargetSource` alone.
The [settings and update lock guards](settings-locks.md) likewise require their
existing command-start locks; asynchronous jobs still need separate coverage.
The [durable generation library and root helper](program-generation.md) now
supply development storage and a bounded process interface for that future
protocol. The source session unit invokes the helper before its presence check;
complete writer coverage and production verification remain open. A stored token
or successful prefix alone is never evidence that a program is free.
The [radio mutation guards](radio-generation.md) additionally invalidate evidence
before publication and owner handovers, including changes that never start a
program. Coordinated apply/hotplug now advance under the apply lock before
derived work. Complete writer coverage and recall exclusion are still required.

[Package entry guards](package-generation.md) now invalidate earlier evidence
before installer/removal/update work, including native release preflight. They
do not exclude writers across a complete asynchronous job or supply the missing
fresh target verifier.

The [provisioning entry guard](provision-generation.md) additionally performs
explicit trusted-parent preparation and advances before a valid full run's
configured steps. Direct helper coverage, complete exclusion and fresh
authority are still required before enabling preview or recall in production.

[Session/native acquisition](session-generation.md) now has its own entry
guards, without gating stopped/unkey/recovery. These do not replace complete
writer exclusion or authoritative checks for pending jobs and routing journals.

The [generation read lease](program-generation.md#shared-read-lease) now holds
a shared lock through a trusted synchronous callback and postcheck, including
after cancellation. It blocks cooperating advances, but cannot exclude a writer
that advanced earlier and is still mutating. The production target source stays
unconfigured until mutation-phase protection and fresh authority are complete.

[Native mutation owners](native-mutation-leases.md) now retain an exclusive
generation lease through approval, start, release maintenance and initialization.
Clients release their stamp before IPC, and resume rechecks its exact receipt
after acquiring external authority. Shell mutation phases, installed binding
and fresh free-state evidence remain open; production authority stays disabled.

The [full provisioning gate](provision-operation-lock.md) now supplies a separate
lock through a valid configured run. A future verifier must account for it
alongside the remaining job/unit/process evidence; its existence alone does not
provide target authority or prove that old child work has finished.

The [update execution gate](update-execution-lock.md) now adds a distinct
full-command boundary for update/install/remove job bodies and their result/
cleanup work. Settings execution, other caller scopes and authoritative fresh
job/process evidence are still required. Legacy result files alone are not
invocation-bound completion evidence.

The [settings execution gate](settings-execution-lock.md) additionally covers
the full apply worker while leaving confirm/revert on their existing lock.
[Settings request/result binding](settings-result-binding.md) identifies the
admitted request for completion presentation. Fresh process/recovery evidence
remains required; a free execution gate alone is not a preset target permission.

The [radio mutation-phase audit](radio-mutation-phases.md) records why extending
apply exclusion across a whole claim can block the hotplug prerequisite of its
session. Phase separation, pending intent and post-wait revalidation remain
required before these caller locks can support recall exclusion.

[Radio publication exclusion](radio-publication-lock.md) now prevents the shared
save phase from changing desired records during a cooperating apply. Its short
scope preserves the session/hotplug ordering boundary; it supplies no permission
for a later radio write and does not close whole-handover caller coverage.

The service captures a continuous tab claim and checks the target/capabilities,
then calls `Poller.Observe`. Before publishing it rechecks the target, freshly
reads registration and definitions again, compares the complete identities and
verifies that local claim/poller evidence remains current. Unsupported range,
unknown evidence, transmit state, missing ownership, cancellation or a changed
source returns a named refusal and no partial successful comparison/token.
Unexpected source errors become a fixed unavailable reason rather than exposing
private paths or process evidence. No logging, CAT writes, ownership acquisition,
program start or definition edits are part of this service.

Tokens use 32 random bytes, remain in memory and expire two seconds from the
start of evidence collection, using monotonic time when available. They bind
the login/tab, selected definition/revision, full registration, target/capability
identity, continuous claim and observed VFO/frequency. `Recheck` performs the
fresh reads again, refuses changes, retires failed evidence and never extends
the original lifetime. A new admitted preview replaces that login/tab/radio's
previous token; a foreign login/tab or different radio cannot inspect or invalidate it. Token
collisions refuse rather than overwrite. Storage is bounded to 256 tokens and
16 in-flight operations, with at most one operation per login/tab/radio.
Expiry is checked on access and stale entries are pruned on requests; no
background timer or radio polling is introduced. Publication samples time again
after local validation, so waiting for a local mutex cannot revive expired proof.

These are advisory read receipts, not transmit reservations or write permits.
The reads are not an atomic hardware transaction. A later recall must revalidate
inside the shared operation boundary and perform guarded write/readback; this
service has no recall or consume-write-permit operation. The HTTP adapter must
still gate every request on the live station session and same-origin policy.

## Preview software evidence

Docker Go vet and the full race-test suite passed. Root-owned temporary fixtures
exercise the actual definition store, registration reader, owner claims and rig
client against an isolated loopback extended-protocol endpoint. The target
verifier and physical radio replies are synthetic, not evidence that production
target qualification, real rigctld, a program lifecycle or hardware has passed.
The suite checks successful comparison/recheck, zero write commands, unchanged
registration/definitions, private-field exclusion, source errors, range/TX/
ownership refusals, stale definition/registration/claim/target/write generations,
frequency/VFO changes, cancellation, token binding/replacement/expiry, entropy
failure/collision and bounded admission/storage. A controlled clock crossing
the deadline during final local validation is refused by both preview and recheck.

`tests/integration/test-preset-preview.sh` separately compiles and runs the suite
in network-disabled Docker, with a read-only root, no capabilities, private
temporary files and no station/hardware sockets or state mounts. Integration
mode requires root, loopback-only interfaces and no radio devices; it fails
instead of skipping a missing prerequisite. The successful local driver result
records its native test executable hash. Repeat invocation refuses and preserves
existing evidence. Bash/ShellCheck and workflow YAML parsing passed. ARM64
test-executable compilation passed; that executable was not run on the Pi.

Ordinary non-root Go test invocations skip the root-owned registration fixtures.
CI/release binary jobs are therefore configured to use the explicit Docker-root
driver with their prepared images. Those hosted workflows have not run here.
Evidence is retained at `/workspace/dxberry-preset-preview-tests/`. Staticcheck
is still unavailable. The HTTP adapter and its later evidence are described
below. Production target verification, UI, physical acceptance, public release
and Pi installation remain open.

## Authenticated preview HTTP adapter

The development daemon now registers exactly two read-only operations:

| Method and path | Exact JSON body |
| --- | --- |
| `POST /api/radio/{name}/presets/preview` | `{"id":"32 lowercase hex characters","revision":"64 lowercase hex characters"}` |
| `POST /api/radio/{name}/presets/recheck` | `{"token":"64 lowercase hex characters"}` |

The daemon intentionally supplies no backend until a qualified production target
verifier exists. A valid request therefore returns unavailable (503), with no
registration/helper construction, filesystem access or radio reads caused by
this adapter. It never falls back to the cached radio inventory. The Pi is not
running this development daemon.

Normal station session/same-origin middleware surrounds both operations. The
adapter also resolves the live session itself and obtains its private login ID
from authentication. It accepts exactly one `X-DXB-Tab` header using the existing
station tab format (1–64 printable ASCII characters), with no body/query fallback.
Query parameters, body login/radio/frequency/path overrides, duplicate/extra/case-
aliased keys, nulls, malformed encoding and trailing JSON are refused before the
backend. The total JSON body limit is 1 KiB. Login identity is never body input.

Recheck now takes the selected radio explicitly in the service API. A token for
another radio is refused before fresh sources are read, without invalidating the
real token. Backend calls inherit request cancellation and a two-second timeout.
Successful responses use `{"ok":true,"preview":...}` and the explicit public
preview field set. The adapter rejects inconsistent or incomplete backend
results and never returns a successful comparison alongside an error. Failure
responses use fixed `code`/`reason` text, without raw process/path errors. Both
success and failure are uncached; busy replies include `Retry-After: 1`.

The adapter registers no recall, undo, apply, tune, PTT, definition-save or
definition-delete action. Definition editing remains the separate configuration
CLI transport. No full-station report refresh is requested by these handlers.

The authenticated preview adapter takes a preset ID and definition revision
for an explicitly selected registered radio. The server loads the definition
and produces a current-to-requested frequency difference. Merely listing,
editing or previewing sends no CAT write, claims no transmitter and starts no
application.

A ready preview requires a fresh trusted radio record, the current poller/link
generation, supported receive range and a fresh confirmed frequency/VFO/PTT
observation. Unknown, unsupported or unreadable PTT state is blocked in this
first recall slice. A currently transmitting radio or an unknown/running program
holder is blocked. Do not substitute the cached last-good record, default zero
PTT, or an optimistic frequency for missing evidence.

Return named blocked/unavailable/conflict reasons using existing station result
conventions. A ready answer includes an opaque, short-lived preview token bound
to the login/tab, preset revision, current target identity, capability result,
selected VFO and observed frequency. Private selector/process metadata stays on
the server. A changed definition, registration, link, VFO, owner or observed
frequency requires a new preview; expiration uses a monotonic deadline.
Owner identity uses the continuous claim generation, not just the same tab ID
after a release/reclaim cycle. The development owner interface now supplies
this local observation; binding it with fresh program/radio evidence remains.

## HTTP adapter software evidence

Docker Go vet and the full race-test suite passed; strict-body fuzzing passed
141,942 cases. Adapter checks cover authentication/same-origin and handler-level
session refusal, exact login/tab/selection forwarding, duplicate tab headers,
body/query/control-route refusals, bounded deadlines and cancellation, fixed
failure responses and inconsistent backend results. The production daemon's
unconfigured adapter remains unavailable by construction.

The root Docker driver now also compiles/runs the HTTP test executable. Its
actual HTTP fixture uses production middleware and adapter, the real service,
definition store, registration reader, owner claims and rig client. It confirms
successful preview/recheck; wrong-login, wrong-radio, logged-out, cross-site and
spoofed-input refusal before fresh sources; registration replacement conflict;
unchanged definition/registration contents; and exactly 24 `get_` protocol
commands with no control writes. Authentication, clock, target qualification
and radio replies are synthetic. This does not establish PAM, effective process
identity, real rigctld or physical acceptance.

Native and ARM64 console/helper builds passed. The console now compiles the
adapter but supplies no verifier/backend; no ARM64 execution or Pi installation
occurred. Bash/ShellCheck passed for the expanded Docker driver. Evidence is
retained at `/workspace/dxberry-preset-preview-http-tests/`. CI/release jobs use
that driver, but hosted jobs have not run here. Staticcheck remains unavailable.

## Recall and undo are separate implementation gates

Recall must use a per-radio operation boundary shared with key-on, tune-carrier
start and owner changes. Revalidate the preview and receive state inside that
boundary before the command. Existing STOP/owed-unkey handling must remain
urgent and able to cancel recall; do not hold a general lock that makes unkey
wait behind a slow preset operation. A physical front-panel PTT change cannot
be prevented by a software mutex, so actual hardware behavior remains an
acceptance item even with a CAT PTT read.

Use the existing frequency intent/range checks and SWR-guard notification;
do not create a second unguarded CAT path. A preset does not claim or take over
a radio automatically. A distinct owner change invalidates the operation.
The new operation boundary belongs outside the existing write callback;
calling `Intents.Freq` from inside `Poller.Write` would reenter its write mutex.
Read back the actual frequency on the same selected VFO/link. CAT acceptance
or an optimistic state update alone is not confirmed recall. If the link is
lost after sending the command, report unknown outcome rather than claiming
that the radio stayed unchanged. Never replay an interrupted recall on restart.

Undo is a new guarded frequency change to the recorded preceding frequency.
It is offered only after a confirmed recall and while the current frequency
still matches that recall, with the same target/VFO and valid ownership/RX
evidence. Front-panel tuning, a newer recall, identity change or an uncertain
result invalidates it. Do not automatically restore a snapshot over someone
else's subsequent radio change. Persistence of the preset library is atomic;
radio operations and undo are not a filesystem transaction.

## Delivery order and evidence

1. Implement the bounded definition parser and configuration store/CLI, with
   malformed-input, revision-conflict, interrupted-write and preservation tests
   in Docker. No recall route or live controls accompany this storage slice.
2. Add the read-only preview service with injected fresh-identity/readback
   sources. Check stale records, replaced pollers, changed definitions, unknown
   PTT/program state, range boundaries and cancellation in Docker.
3. Add the shared operation boundary and guarded recall/undo, with deterministic
   key-on/STOP/owner-change interleavings, timeout and uncertain-outcome tests.
   Verify the actual rigctld protocol using an isolated non-RF fixture.
4. Design and implement the interface after the backend contract is reviewable.
   Check loading/error/conflict/unknown outcomes, keyboard/phone operation and
   that preview emits zero write intents. Browser fixtures do not establish RF.
5. Plan current-radio acceptance and a reversible Pi deployment separately.
   Preserve the manual Graywolf arrangement and working radio/TX settings.

Storage, configuration transport and the injected preview service are implemented.
Production target verification, preview UI, guarded recall/undo and hardware
checks remain separate gates. Program/VFO profiles, macros and automatic mode
selection retain their own later designs.
