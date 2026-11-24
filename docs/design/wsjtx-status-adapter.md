# WSJT-X status adapter

This is the first implementation slice of native program status in the
[continuation roadmap](project-roadmap.md). It is a backend design; the adapter
is not present in the running station yet.

## Implementation state

The local backend now has `station/internal/wsjtx.Parse`, a standalone bounded
reader for Heartbeat, Status, Decode, Clear and Close. It accepts Qt schemas 2
and 3, keeps null/absent values explicit, rejects malformed or oversized fields,
and ignores unsupported message types without manufacturing an observation.
The minimum Status layout ends at DX grid; later fields are optional. Decode
requires the message text and keeps later confidence/off-air flags nullable.
The parser opens no sockets and sends no program or radio command. Separate
WSPR-decode and contact-log message types are ignored in this first subset;
contact logging continues through the existing bridge.

The source inventory is now pinned to the installed application's v3.0.2 tag.
Its protocol header is byte-identical to the previously inspected header. Qt
5.15.15 on the Pi matches the runtime used inside the isolated fixture container.
The [wire fixture record](../../station/internal/wsjtx/testdata/README.md)
distinguishes this source/serialization evidence from a running-program test.
The local tracker now implements per-radio generations, expiry and bounded
history, as described below. A receive-only loopback observer, durable endpoint
history, a routing/restoration policy, settings document/file primitives and
a durable routing journal, cross-user settings bridge and program/session
controller are implemented locally. The controller now composes those pieces
with a concrete systemd/process guard. The daemon, root unit hooks, persisted
explicit opt-in and authenticated backend routes are now connected locally.
Update/rollback protection is now implemented locally with a durable acquisition
pause and explicit bundle compatibility contract. Bounded native status events
and the read-only native display are connected locally. Running-program
compatibility and CPU/capture acceptance remain before activation. No adapter is enabled on the Pi.

All Go packages passed vet and race tests in containers. A 20-second parser
fuzz run completed 1,078,178 executions without failure, including the 18 Qt
fixtures as seeds. It is not a substitute for lifecycle or running-program
compatibility checks.

## Session tracker implementation

`NewTracker` creates an in-memory observation store. `Start` returns an opaque
receiver token for one radio generation; restarting the same instance ID returns
a new token and clears the previous observation. Late packets and stop callbacks
carrying an old token cannot update or remove the replacement. Instance IDs are
checked within each radio, so two separately routed radios can use identical
program IDs without sharing state.

The store exposes waiting, live, stale and closed states. Valid recognized
traffic refreshes protocol liveness; malformed, oversized, unsupported and
wrong-ID traffic does not. Expiry is 45 seconds (three verified heartbeat
periods), measured with the clock's monotonic time. A timeout clears current
status, version/schema and decode history and advances a continuity epoch.
Reconnect starts with unknown readings until new Status/Decode messages arrive.
A heartbeat does not manufacture status or reset its separate arrival timestamp.
Close is terminal for the token; Clear discards only band-activity history.

History retains at most 200 events per session, with oldest-event eviction and
explicit counters. Exact live duplicates merge only within 45 seconds of first
arrival. Replays merge with the newest retained matching event; the record keeps
both live/replay observations and its first/last arrival times. Confidence,
off-air and null values remain part of event identity. The program's time-of-day
is never converted into an invented calendar date. Up to 32 observation sessions
bound retained memory; removed sessions leave no growing tombstone list.

Snapshots deep-copy all mutable fields. Sixteen tracker tests cover two
radios (including identical IDs), expiry boundaries and reconnect, rejected
traffic, bounded history, replay semantics, Clear/Close, replacement/removal,
registration limits, snapshot mutation and concurrent receivers/reads/restarts.
All Go packages passed vet/race checks in containers. UDP routing, actual
program lifecycle observation and physical two-radio operation are separate
acceptance steps.

Expiry is checked on reception and snapshot reads; the event producer now
schedules periodic snapshot refreshes so an idle connection's expiry is
published even when no more packets arrive. No timer or network listener runs
in the current tracker itself.

## UDP observer implementation

`NewObserver` owns the tracker and its sockets. `Start` binds only
`127.0.0.1` over IPv4, obtains an unused ephemeral port and captures the returned
radio-generation token. It returns an opaque binding for the future program
configuration layer. No user-selected host, public interface or outbound sender
is exposed. The first valid accepted packet selects a loopback source address
and port; later packets from another source are counted and rejected. Malformed,
wrong-ID, unsupported or delayed input cannot select that peer.

A fixed 8 KiB port bitset prevents reuse, including after Stop or replacement.
The basic constructor keeps it in memory; the persistent constructor described
below retains it across observer/process restarts. Bind attempts are capped at
64. Failure preserves the working socket and generation; there is no fallback
to a shared, reused or wildcard endpoint. Recovery of existing program routing
still needs verified generation/settings reconciliation before activation.
The receiver captures its token at socket creation and cannot adopt an old
packet by looking up the newest token.

Each receiver requests a 256 KiB OS receive buffer (the OS may cap or adjust it)
and has a 32-datagram application queue. Full-queue packets are dropped without
allocating another copy. Queue drops count only application discards, not
unobservable kernel/network loss. Reading one byte beyond the 16 KiB protocol
cap prevents an oversized datagram from being accepted as a valid prefix.
Queued copies are drained on closure; socket and goroutines are joined on Stop,
replacement and observer shutdown. Unexpected socket failure clears current
readings immediately and records a transport error.

Observation timestamps are captured when the socket read returns, independently
of later queue processing. Delayed, future or out-of-order observations do not
refresh state or select a peer. These are observer-read timestamps, not kernel
arrival or program-send timestamps. This prevents an application backlog from
making old status appear freshly received. Peer publication and the first
accepted observation are synchronized for snapshot readers.

Sixteen UDP/queue tests cover actual loopback delivery with the Qt fixtures,
no replies, same-ID radios, endpoint rotation, late stop callbacks, source
pinning, oversized prefixes, failed binds, retired/exhausted ports, wildcard
refusal, socket failure, Close, queue ownership/capacity, delayed delivery,
shutdown and concurrent replacement. They do not run WSJT-X itself. Fetching the
official 3.0.2 binary for an isolated compatibility test currently returns a
network 503; that check remains unverified. No live program configuration or
radio was changed to work around the unavailable download.

## Endpoint history across daemon restarts

`CreateEndpointHistory` initializes a new private directory explicitly.
`NewPersistentObserver` only opens an existing valid history and holds an
exclusive nonblocking ownership lock until its receivers have closed and joined.
There is no create-on-open or automatic reset when a journal is missing,
corrupt, unsupported or exhausted. Station integration must use this constructor;
`NewObserver` remains an in-memory option for isolated fixture use.

The versioned 8,232-byte record contains only the port bitset and a SHA-256
integrity checksum. It contains no decoded traffic or program settings. Each
candidate is retired by writing a private staging file, syncing it, renaming it
over the journal and syncing the directory, before publishing a replacement
generation or returning its binding. The parent directory is synced at initial
creation too. Exactly one fixed staging name bounds crash leftovers; pending
content is never used to reconstruct history. A later write replaces the orphan
under the ownership lock. Clean and unclean process exits retain committed ports.

Missing/replaced directories or locks, unexpected history changes and uncertain
writes prevent new bindings. A storage failure closes the candidate socket and
preserves the current receiver. The owner stays in an error state until closed
and reopened; it cannot continue with an uncertain in-memory inventory. Files
must be private regular files owned by the current user, with one hard link;
final-component symlinks and nonregular files are refused. The state parent is
trusted and ownership locking coordinates cooperating processes.

The checksum detects damage, not deliberate rollback to an older valid record.
Installation, restore and rollback must retain the latest endpoint history.
Deleting it or restoring an old copy requires a separate recovery procedure
before any program routing can resume. Ports are never aged out automatically;
bounded bind attempts or inventory exhaustion yield an explicit unavailable state.
Persistence writes happen on session creation, outside packet reception.

This component still does not edit program settings, reconcile a running
program after restart, persist session identities or expose a browser API.
Program route leases, profile conflict handling and explicit opt-in remain
necessary before activation. Durability tests exercise the filesystem protocol
and an exit without cleanup; they are not a physical power-loss test on Pi media.

Twelve endpoint-history tests, with separate corruption and failure subcases,
cover clean restart/retired traffic, unclean exit, publication ordering, exclusive
ownership, missing/corrupt/unsafe files, replaced state, failed writes before and
after replacement, socket cleanup, concurrent reservations, exhaustion and crash
staging. All Go packages passed vet/race checks in containers, and the WSJT-X
test package compiled for Linux arm64. No Pi binary or program setting changed
for this component.

## Verified 3.0.2 protocol configuration

The matching `Configuration.cpp` reads `UDPServer` (default `127.0.0.1`),
`UDPServerPort` (2237), and `AcceptUDPRequests` (false). Its N1MM logging port is
2333, independently configured. The client sends heartbeats every 15 seconds,
initially with schema 2 while advertising a maximum schema of 3. It records a
server's negotiated schema before checking whether control requests are enabled,
so schema negotiation does not require enabling program-control requests.
These are verified source defaults and behavior, not a claim about an
operator-edited settings file or a completed running-program compatibility test.
No settings have been changed for the adapter.

## Useful first behavior

For each radio running WSJT-X, expose current program availability, reported
mode and dial frequency, reported transmit/decoding flags, and recent decodes.
Keep the existing full program window. The adapter observes program state;
physical radio state and the station's transmit-ownership decision remain
separate facts.

The current module creates a separate `WSJT-X - RADIO.ini` for every radio and
starts the program with `--rig-name=RADIO`. Its N1MM-compatible logging target
is loopback UDP 2333. That is a contact bridge, so do not repurpose it for this
adapter or interrupt the existing logbook path.

## Protocol boundary

The upstream `Network/NetworkMessage.hpp` describes a big-endian Qt datagram
protocol with magic `0xadbccbda`, schema, message type, and instance ID. UTF-8
strings have a 32-bit byte count; zero means empty and `0xffffffff` means null.
They are not Qt UTF-16 strings. Floating-point values use 64-bit precision,
including the decode time offset described as a float in the message table.

Start with schemas 2 and 3 for the fields actually parsed. Treat other schemas
as unsupported. A missing trailing optional field is distinct from a truncated
required field. Ignore unknown message types and appended fields in supported
schemas, as the upstream compatibility contract requires. Never allocate from
an unchecked wire length.

The first allowlisted incoming types are:

| Type | Purpose | Handling |
| --- | --- | --- |
| 0 Heartbeat | Instance/version and liveness | Track reported maximum schema and program version; do not assume schema number is an application version |
| 1 Status | Mode, frequency and program flags | Replace the snapshot atomically; preserve unknown/unreported values |
| 2 Decode | Completed or replayed decode | Retain new/replay, UTC time-of-day, SNR, time/frequency offsets, mode, text, confidence and off-air flags |
| 3 Clear | Program discarded band activity | Clear this generation's decode ring |
| 6 Close | Program is shutting down | Mark disconnected immediately and retire this generation |
| Other | Unsupported or later extension | Ignore without modifying the current snapshot |

A decode's `QTime` is milliseconds since midnight, not a complete timestamp.
Keep arrival time separately and expose the program's time-of-day without
inventing its calendar date. Off-air decodes must stay visibly marked; low
confidence must not be lost in serialization.

The matching 3.0.2 sender writes these messages whenever its destination is
resolved and its port is set; sending does not wait for a server heartbeat.
The observer reads the initial schema 2 directly and sends no negotiation or
control messages. Outbound protocol operations remain outside this slice.
It is a program-status observer, not a physical emergency-stop mechanism.

## Configuration preflight findings

The v3.0.2 command-line parser provides rig selection and selection of an
existing configuration, but no UDP destination override. Routing will need a
reversible settings change while the program is stopped. Native station routing
is not connected or enabled yet.

Real-Qt checks found a defect in the existing shared one-key INI helper: it could
write and verify a first occurrence while Qt used a later duplicate or an escaped
alias. The helper now refuses ambiguous/noncanonical layouts before editing the
affected field. Thirteen new shell regressions fail against the preceding
helper. All 888 shell regressions, ShellCheck/syntax and the build-tree check
pass, with ten independent checks using Qt 5.15.15. Those Qt checks also confirm
a normal update and a single-line update of the real first-run fixture while
preserving all other bytes. QtCore alone cannot instantiate the fixture's
application-specific QVariant types; the test checks the UDP string and keeps
those opaque values untouched.

The helper fix (`034ee5a`) is installed on the Pi with rollback at
`/root/dxb-ini-conflicts-034ee5a`. Program/radio/network configuration, mixer,
core processes and console binary matched before and after. All nine status
sections answered successfully afterward, and transmit remained enabled.
Only `lib/inifile.sh` was replaced; native routing is still inactive.

Qt loads sections lazily. A complete key enumeration can expose a root-group
alias that a targeted lookup alone has not loaded. The reference test enumerates
the namespace, as the matching program's profile code does. This is not evidence
that every running-program configuration path has been exercised.

The matching profile implementation can save and replace whole configuration
sets. Routing ownership and restoration must account for these profile changes,
operator changes and daemon restarts. An unchanged/default UDP destination does
not prove no other program is using it. Keep explicit per-radio opt-in and
conflict handling; do not silently replace an existing UDP integration. The
program settings helper runs as the application user; that ownership boundary
must be preserved when routing is connected to session preparation.

## Sessions and configuration

### Routing preview and restoration policy

`PreviewRouting` takes a current receiver binding and a complete, verified
logical routing view: active profile, both raw UDP values and every saved
profile's pair, including absent values. It produces a fingerprint bound to the
observer lifetime, radio, generation, endpoint and complete view. A default or
absent UDP destination never implies permission to replace another integration.
`ClaimRouting` requires approval of that exact preview and derives the new
destination only from the receiver's IPv4 loopback binding. This is future
product opt-in behavior; no live opt-in setting or route is enabled yet.

Even a never-published listener port can already be a program destination.
Preview therefore refuses a port configured in the active or any saved profile,
regardless of host spelling. The controller must stop and retire that candidate
and obtain another before proceeding, without exposing its observations as an
active collection. Missing ports use the verified 2237 default; only plain
decimal ports or an explicit empty/zero disabled value are accepted in this
first policy. Unknown encodings are a layout error, never guessed.

The returned opaque `RoutingPatch` is a two-key compare-and-swap plan.
`Apply` computes a replacement settings view only if the complete routing
fingerprint still matches. It leaves saved profiles untouched. `RoutingLease`
captures a deep copy of the original raw pair, preserving absence, explicit
empty strings and original spelling. `Restore` plans both original fields
together only while the managed pair and profile namespace still match. An
already-original view is an idempotent no-change plan.

Profile switches, clones, renames, deletions, saved-route edits or a change to
either active routing field produce an explicit restoration conflict with no
patch. Equivalent raw reserialization is also treated conservatively as a
change. There is no partial restoration, guessed profile origin or overwrite
of operator edits. A failed restoration must retain the durable lease for
review; it must not be reported as released merely because collection stopped.
Old previews cannot follow a new radio, binding or observer lifetime, and an
old lease cannot restore over a newer destination.

This policy accepts bounded, already-verified routing views; it does not parse
an INI file or prove that a reader enumerated the complete Qt namespace. The
limits are 32 saved profiles, 256-byte flat profile names and 4 KiB per raw value.
The shared one-key guard alone is not a complete profile reader. The future
settings bridge must preserve the application-user boundary, reject unknown
aliases/layouts, stop the program and serialize its lifecycle, recheck binding
and file state at the write boundary, and replace both keys atomically while
preserving unrelated bytes. The in-memory lease is not a durable journal or
proof that any write completed. The durable journal below retains ownership
across interrupted transactions; application-user lifecycle integration and
operator resolution of profile conflicts remain before activation.

Fourteen routing-policy tests cover absent/empty/raw-value restoration,
explicit preview approval, profile and route conflicts, pairwise compare-and-swap,
idempotence, old bindings/leases, observer lifetimes, deep-copy isolation,
fingerprint stability, preexisting destination ports and bounded invalid input.
All Go packages passed vet and race checks in containers after the final input
length guard, and the WSJT-X test package compiled for Linux arm64. These are
policy tests using verified-view fixtures, not INI write or running-program
compatibility tests. The Pi's program settings and binaries were unchanged.

### Settings document and atomic file primitives

`ParseSettingsDocument` now reads the complete logical routing namespace from
a bounded Qt INI document, including saved profiles without UDP fields. It
recognizes ASCII percent/Unicode key escapes to detect aliases, rejects duplicate
decoded keys/sections and requires canonical current routing/profile metadata.
Saved-profile paths must have flat, nonempty components; separators are not
silently normalized into another profile inventory. Legacy migration markers,
unbalanced multiline quotes, physical continuations and unsupported names/layouts
produce a layout error, preserving the original file.

This first reader supports UTF-8, LF-terminated files with ASCII key/profile
names, at most 1 MiB, 16,384 lines and 64 KiB per line. Unicode or quoted profile
names, CRLF, BOM and unterminated files are explicitly unsupported. Unrelated
values, including opaque QVariant data, remain byte-for-byte untouched.
`WithPatch` applies the policy's two-key comparison to that document, replacing,
adding or removing only current `UDPServer` and `UDPServerPort` lines. Existing
logging and remote-control settings are not part of the patch.

`OpenSettingsFile` holds a cooperative nonblocking lock and requires the
directory and original regular file to belong to the current user. It does not
elevate ownership. The current primary service group may retain write access,
matching the installed account's 0770 settings directory. Everyone-write or a
writable foreign group, final-component symlinks,
multiple hard links and nonregular files are refused. The higher-level state
parent is trusted; this is not protection against a privileged process replacing
files during a filesystem call. The bridge derives its filename from the
validated radio and runs as the application user.

`SettingsFile.Apply` requires a lifecycle/recovery check before staging and again
before replacement. That check must establish a quiescent stopped program,
exclusive lifecycle ownership, a prepared durable recovery journal and the
current binding on acquisition. The local `ProgramController` now supplies the
combined checks; the local daemon and unit hooks now call it through the private
root connection described below. No such integration is installed on the Pi.
The file library cannot independently establish those cross-process conditions.

The writer starts from the latest complete file, so earlier unrelated preference
edits are preserved. It creates one fixed staging file, preserves owner/group/mode,
syncs the file, and checks the original inode, full bytes and metadata again.
It verifies the staged inode/content, renames the whole file and syncs the
directory before read-back. This exposes both fields together to independent
readers. Replaced paths/locks, new preferences or changed metadata during the
transaction cause refusal. Stale staging is never treated as committed state;
one fixed name bounds crash leftovers and is unlinked without following a link.

Rename or directory-sync uncertainty produces `ErrSettingsUncertain` and prevents
further writes by that handle. It never silently claims rollback or discards
the caller's recovery record. The durable journal below supplies ownership and
tested reconciliation of these file transactions. The local controller/service
now connects the lifecycle checks; real-program and deployment acceptance still
precede use on the station. These primitives alone cannot establish quiescence.

Document/file tests cover the real first-run file,
absent/raw-value round trips, complete profiles, alias/continuation refusal,
bounds, ownership and locking, lifecycle refusals, concurrent file/metadata
changes, staging links, injected storage faults and independent atomic readers.
The new parser completed 836,296 fuzz inputs in 20 seconds without failure.
All Go packages passed vet/race checks and the WSJT-X test package compiled
for Linux arm64. Twenty-one independent Qt 5.15.15 checks passed as a regular
user, verifying effective destinations, unchanged saved profiles/logging/control
settings, exact restoration and refusal of ambiguous/unsupported layouts.
QtCore still cannot instantiate the first-run fixture's application-specific
QVariant types; their bytes are preserved. This is settings compatibility,
not a running-WSJT-X or physical-media power-loss test. Nothing was installed
or activated on the Pi for this slice.

### Durable routing ownership journal

`CreateRoutingJournal` explicitly initializes a new private directory;
`OpenRoutingJournal` requires an existing valid journal and holds exclusive
ownership. It never interprets missing/corrupt state as an empty catalogue or
resets it automatically. The versioned canonical JSON envelope contains a store
identity, revision, transaction IDs, original routing namespaces, managed pairs
and an integrity checksum. Unknown, duplicate, case-aliased or noncanonical JSON
fields and unsupported records are refused. At most 32 records and 1 MiB of
encoded state bound storage; completed records are removed without a growing
tombstone list. Revision exhaustion is an explicit error.

| Durable state | Meaning | Next permitted step |
| --- | --- | --- |
| Prepared | Original routing and the exact proposed binding are recorded before settings may change | Confirm a freshly read managed view, or prepare restoration |
| Installed | The managed settings were observed with the original binding still current | Prepare restoration from a freshly verified original/managed view |
| Restoring | Cleanup intent is durable before original settings may be written | Remove the record only after the complete original routing view is observed |
| Record removed | Restoration was verified and the release was durably recorded | A new approved transaction may be prepared |

`Prepare` accepts only a lease for the exact opaque binding, including its
observer ownership. The listener must have persistent endpoint history and its
port must already be durably retired. A matching address/generation from another
observer cannot inherit approval. `PermitInstall` and `PermitRestore` check the
durable phase; installation also checks that the original persistent binding
remains current. These checks supplement program quiescence and lifecycle
serialization; they do not prove either condition by themselves.

Reopening creates fresh callback tokens and requires restoration for every
outstanding record, even if its stored phase is Installed. No old endpoint is
adopted as a running collector. `PrepareRestore` first persists cleanup intent,
then returns the existing conflict-aware restoration patch. `CompleteRestore`
requires a freshly verified original namespace before removing ownership.
Already-original settings support idempotent cleanup; profile/route changes
retain the record and refuse a patch. Old-owner or old-transaction callbacks
cannot mutate a replacement.

All journal mutations use the same private directory, locking, file-sync,
atomic-replace and directory-sync primitives as endpoint history, in a separate
directory. The endpoint-history format is unchanged. One staging name per store
bounds crash leftovers, and pending contents never reconstruct committed state.
Failed or uncertain journal writes prevent further operations, including reads
that might falsely report a free radio, until close/reopen and reconciliation.
Outside changes to the journal or ownership lock also make the owner unavailable.
The checksum detects damage, not a deliberately restored older valid checkpoint;
backup/update/rollback must retain the latest journal and endpoint history.

Fourteen substantive journal tests cover actual file install/restore with phase
checks, conflict retention, exact binding ownership, reopened recovery, stale
callbacks, capacity/revision limits, concurrent preparation, unsafe/corrupt files,
and injected write failure before/after every journal transition. Six subprocess
exits without cleanup cover preparation, settings write, installed confirmation,
restoration intent, original-file restoration and completed release. These tests
recover the exact original INI and retain retired endpoints. The journal decoder
completed 936,553 fuzz inputs without failure. No software fixture establishes
physical media power-loss behavior or a running-program lifecycle.

All Go packages passed vet/race checks and the WSJT-X test package compiled for
Linux arm64. Existing rig-poller fixture startup races found during validation
were corrected by installing initial interest/hooks and subscriptions before
polling, and waiting for the initial fake-clock cycle to schedule its next tick.
Five affected tests passed 25 repetitions each before the final full-suite pass.
These synchronization changes are confined to test setup.

This is still local backend work. Observations must come from the verified
application-user reader while the controller holds the lifecycle lock, not from
browser-supplied snapshots. The local service now connects stop/start ordering,
opt-in persistence and authenticated backend routes. Actual program compatibility remains; later sections record the completed
events and read-only display. The journal is not an application control endpoint and starts no
radio, program or collector on the Pi.

### Cross-user settings bridge

`SettingsBridge` starts the fixed `dxberry-wsjtx-settings` helper with the
configured `dxbapps` UID and primary GID. Both must be unprivileged. The parent
must be root, and the helper plus its final directory must be root-owned and
protected against replacement by the application account. Root/set-ID helper
execution, a root primary group, changed executable permissions and final
symlinks are refused. The child receives only its primary service group and a
minimal environment. Parent death and caller cancellation terminate the helper.

The helper refuses root before opening a file. Its only inputs are a trusted
application home argument and bounded canonical private-pipe messages for
inspect/install/restore. A validated radio selects
`HOME/.config/WSJT-X - RADIO.ini`; the message cannot supply a filename,
executable, shell method or arbitrary install destination. Install targets must
be canonical IPv4 loopback with a nonzero port. Restore values come from the
controller's opaque restoration patch and are checked by the document/file
writer. The helper is absent from the browser command allowlist and opens no
listener.

On a write, the helper asks its parent for a commit decision before staging and
again immediately before replacement. The parent invokes its supplied guard at
each boundary. Idempotent writes still require both checks. The guard must
combine the journal permit with real program quiescence and exclusive lifecycle
ownership; a journal permit alone is insufficient. The local controller now
supplies that combination. The local root hooks are connected through a private
socket; they have not been deployed or exercised with real systemd on the Pi.

Success requires a freshly read matching profile/destination, process success
and end-of-stream without extra output. Frames are capped at 2 MiB; duplicate,
unknown and case-aliased fields and incorrect stage order are refused. Stderr
and raw settings values are not returned as diagnostics. The default exchange
limit is five seconds; an earlier caller deadline takes precedence. Every
failed write after helper startup is conservatively uncertain, retaining the
recovery record even when the file may already have changed.

Nine bridge tests cover actual UID/GID separation, denial of root-private state,
discarded parent environment, journal-backed real file changes, the standalone
binary, idempotence, denied checks, early exit/oversized/extra/inconsistent output,
cancellation after a proven write, earlier deadlines and unsafe identities or
requests. Two additional file tests verify retention of primary service-group
write access and refusal of writable foreign groups. The Pi's existing 0770
directory and 0644 files were inspected read-only; their permissions and contents
were not changed. The frame decoder passed 415,849 fuzz inputs. All Go packages
passed vet/race checks, and the helper and WSJT-X test package compiled for
Linux arm64. The console and native helper also built successfully with the
normal build script; the helper uses CGO disabled.

`station/build.sh` builds both executables. CI/release artifact paths carry the
helper, and a dedicated Docker integration script builds and runs the cross-user
checks with the actual standalone binary, including where ordinary nonroot
runners skip privileged fixtures. All 890 shell regressions, ShellCheck,
syntax/build-tree checks and workflow YAML parsing passed locally. Remote
CI/release jobs were configured, not invoked. No helper was installed on the Pi;
collection, program lifecycle and opt-in remain inactive.

Use a distinct loopback listener for each active radio/program generation.
A process restart, owner change or radio removal invalidates its snapshot and
ring. Match datagrams to the expected local endpoint and instance identity;
a delayed packet from a retired generation cannot make a new session connected.
Bind failure is an explicit unavailable state, with no fallback to a public
interface or shared ambiguous port. A receiver must capture the token at socket
creation, rather than looking up the newest token when each datagram arrives.
Tokens reject late callbacks; they are not wire-level generation identifiers.
A reused UDP endpoint receiving a delayed same-ID datagram cannot distinguish
that datagram's old generation, so endpoint rotation and reuse policy must be
implemented and tested by the receiver before activation.

Before writing settings, verify the exact installed version's UDP configuration
keys, default destination, heartbeat interval and remote-control switches. Existing
operator UDP integrations may already use the destination. Preserve them; surface
an explicit configuration conflict rather than silently replacing the destination.
The present wiring contract owns only audio/CAT/PTT keys after first fill, so
adding protocol routing needs an explicit versioned configuration change with
backup and restoration. Never edit a running Qt settings file behind the program.

Do not register the current manual APRS CODEC merely to test this adapter. Parser
and loopback tests can run in a container; installed-program compatibility can
use an isolated software-only instance when its audio/control behavior is proven
safe. It must not claim or rename the active station sound card.

### Program controller and stopped-program guard

`OpenProgramController` now composes the persistent observer, routing journal,
application-user settings bridge and a concrete systemd/process guard. Opening
requires both existing valid state stores and a fresh radio-eligibility callback;
it never initializes or repairs state. Acquisition requires a registered, present
radio assigned to WSJT-X. Restoration works even after reassignment or unplugging.
The local daemon and root program hooks now call this controller through
`ProgramService` and the root-only socket described below.

`Preview` requires an inactive/failed program unit with no remaining application
processes and refuses outstanding recovery. Its fingerprint binds the radio and
complete original routing namespace, including all saved-profile routes. The
caller must explicitly obtain and persist operator approval; `ProgramService`
now supplies that persistence. Returned settings are private control-plane
data for an authenticated preview, not public status or log content.

`Start` requires the exact systemd invocation at `ExecStartPre`. It restores any
old outstanding route first, verifies the approved original namespace, allocates
a fresh durably retired endpoint and records preparation before calling the
application-user helper. At both helper checks it verifies lifecycle, current
radio eligibility and the exact journal/binding. Fresh read-back is required
before the installed phase is committed. A duplicate start for the same live
invocation is idempotent; a missed stop is restored before replacement. Changes
to any original profile/route invalidate approval.

`Stopped` matches the exact invocation at `ExecStopPost`; a delayed callback
cannot remove a replacement session. Restoration retires the receiver, records
cleanup intent and checks lifecycle/journal at both helper boundaries. Conflicts,
denied checks and uncertain writes retain recovery evidence. `Recover` requires
an inactive/failed unit. `Close` retires sockets and releases ownership without
editing settings, since the program may still run. After reopen no old UDP
binding is adopted; recovery precedes the next fresh allocation.

The guard queries only the fixed `dxberry-screen@wsjtx-RADIO.service` unit using
direct bounded `systemctl show` execution. It requires the exact unit, loaded
state, phase, invocation and zero main PID. It also checks nested cgroups for any
remaining application UID, checks manually launched WSJT-X processes using the
same settings identity, and queries the manager again after process inspection.
Only simple verified rig-name forms for an explicit other radio are ignored;
ambiguous arguments, unreadable/oversized evidence or manager transitions cause
refusal. Root hooks can remain in the cgroup; application processes cannot.

Controller operations serialize across radios with a context-aware lock and a
30-second default limit. Earlier caller deadlines include time waiting for that
lock. The manager check has a two-second limit; the existing helper limit is
five seconds. Process-file reads and inventories are bounded. This coordinates
managed sessions and cooperating configuration writers, not a hostile privileged
process or an operator deliberately racing a manual launch/configuration edit.
The root hooks hold the start-pre/stop-post barrier until the controller returns
and prevent program startup after acquisition failure.
They must also preserve the existing radio-record lock order rather than taking
that lock recursively while a waiting start command already holds it.

Fourteen controller tests and eight guard tests exercise real application-owned
settings and the standalone helper, actual loopback fixtures, concurrent radio
transactions, both denied write boundaries, stale callbacks, missed stops,
restart recovery, operator conflicts, journal faults before/after replacement,
uncertain settings writes and a deadline while another transaction is held.
The guard tests include actual cross-user process identity, nested cgroup and
manager fixtures, and a composed controller/guard/bridge transaction. The manager
is a fixture and the extra process is inert; these checks do not run WSJT-X or
establish real systemd lifecycle compatibility on the Pi. No daemon hook, program
setting or adapter was activated on the Pi.

All Go packages passed vet and race tests in Docker with the standalone helper
supplied. The console/helper built natively, and the helper and WSJT-X test
package compiled for Linux arm64. The expanded standalone integration script
passed with networking disabled, along with ShellCheck and shell syntax checks.
An existing poller test also exposed a subscriber-history race: it now waits
for the transmitted and subsequent zero frames to be consumed, and joins its
test reader on cleanup. Its original meter/cadence assertions remain; 25 repeated
race runs and the full suite passed. No production poller behavior changed.

### Daemon hooks, saved approval and backend routes

`dxberryd` now owns a local `ProgramService` and the root-only Unix socket at
`/run/dxberry/station-programs/wsjtx.sock`. The runtime directory is 0700, the
socket is 0600 and both peers verify Linux root credentials. An exclusive runtime
lock prevents a second daemon from replacing the socket; unsafe existing entries
are refused. Up to eight clients have bounded canonical messages, read deadlines
and operation limits. The socket accepts only named start/stopped callbacks,
radio and systemd invocation. It accepts no destination, approval, path or command.

The existing binary also serves as the root hook client using `-wsjtx-hook`,
`-wsjtx-radio` and `-wsjtx-invocation`. `dxberry-app prepare` calls it after the
existing application-user settings, wiring, companion/audio and rig-wait steps;
an error prevents the application from starting. `stopped` performs its existing
cleanup/unkey first, then requests restoration even when the radio record cannot
be loaded. A settings/recovery failure cannot skip that cleanup. No extra
record lock is taken by the callback, preserving the waiting start command's
lock order.

An unavailable daemon can be skipped only when strict preferences show collection
disabled and either no inventory was configured, or valid exclusively readable
endpoint history and routing journal prove this radio has no owned route.
Missing/damaged/locked configured state or a pending route blocks startup.
Daemon shutdown cancels in-flight requests; it preserves original routing evidence
without editing a potentially running program.

Collection stays off by default. The root-only `-wsjtx-initialize` mode explicitly
creates a new `state/wsjtx` inventory; it refuses existing or partial state. Its
trusted station parent must already exist. New station/inventory directory entries
are synced. This setup command is not a reset/recovery operation and has not run
on the Pi. Consent is stored per radio as `wsjtx_routing_approval` in the existing
station settings file, containing the radio/complete-original-namespace fingerprint.
Enable obtains a fresh stopped-program preview and compares it with the approved
fingerprint before persisting. Disable requires stopped recovery before clearing
consent. An uncollected start still restores any previous managed route first.

Strict preference reads distinguish absence from damage, rejecting unsafe files,
duplicate/case-aliased/unknown keys, null/invalid fields, trailing data and oversized
documents. Approval and ordinary settings writers share the store lock and preserve
each other's fields. A damaged document is not replaced with default transmit
limits. File, directory and parent syncs follow replacement. The ordinary SWR/PTT
reader retains its existing fallback behavior and ignores the collection field
entirely, including malformed field types; it cannot supply fallback consent.

Acquisition reads current radio assignment, invokes the existing authoritative
presence check (`verify-present`, which does not set a waiting/restart marker)
with a minimal environment and a two-second bound, then verifies
the record is unchanged. Cached UI records alone do not authorize a new route.
Stop/recovery remains independent of a changed assignment or damaged approval.

Authenticated, same-origin backend routes are now registered locally:

| Route | Behavior |
| --- | --- |
| `GET /api/radio/{name}/wsjtx` | Bounded current collection state and detached program observation; no original destinations or private transport/journal identity |
| `POST .../wsjtx/preview` | Fresh private destination/profile preview while stopped |
| `POST .../wsjtx/enable` | Accept only the approved fingerprint and recheck the preview |
| `POST .../wsjtx/disable` | Restore stopped routing before clearing consent |
| `POST .../wsjtx/recover` | Conflict-aware stopped recovery |

POST bodies contain only `{}` or the single enable fingerprint; duplicate/aliased
keys, extra fields and oversized requests are refused. Errors use bounded reasons
and recovery guidance, without copying raw settings or internal errors. No HTTP
start, stop, transmit, tune or UDP-send route is added. The read-only display described below consumes status events; its refresh does
not call these configuration routes.

Twenty-four additional Go tests cover the service/socket, settings preservation,
fresh eligibility, command modes and authenticated HTTP boundary. Nine shell
regressions verify callback arguments, settings-before-hook order, blocked starts
and cleanup-before-recovery, including record-load failure. The socket tests use
real root/application-user credentials and the standalone helper. Systemd states
remain fixtures; neither WSJT-X nor real radio equipment runs in these checks.

All Go packages passed vet/race checks in Docker. After the final directory
durability refinement, the four affected packages passed race tests again, with
all-package vet, native console/helper builds and arm64 helper/test compilation.
All 899 shell regressions, ShellCheck, syntax and build-tree checks passed in the
final rerun. The read-only presence command is covered separately from the unit
condition's hotplug marker behavior; help assertions check the rendered output.
The affected main Go tests and console build passed after that correction. The
expanded standalone helper/socket integration passed with networking disabled.

## Update and rollback acquisition guard

The updater swaps trees before program restart, and older trees have no native
restoration hooks. Before an incompatible update or rollback, it now persists
a station-wide pause, then proves every managed route has been restored. A
running/uncertain/conflicting route refuses the tree change; the guard never
edits a running program's settings or stops a session to make the audit pass.
Existing receivers can continue while paused. Stop and recovery remain available;
initialization, approval and new route acquisition are refused.

The root-private `station/wsjtx-release` directory sits beside the native
inventory, outside `/opt/dxberry` and its backup trees. Its bounded, canonical,
checksummed `release.v1` has a revision and a fresh opaque receipt for each pause
attempt. Missing files in an existing directory, damaged state, lock contention
and unknown ownership fail closed. The pause survives daemon restart, failed
rename/setup and rollback to a legacy tree. It is never cleared automatically.
Retain the latest release gate, endpoint history, journal and approvals through
backup, upgrade and rollback. Checksums detect damage, not deliberate replacement
with an older valid checkpoint.

Compatible publishers declare `share/wsjtx-native-protocol` as `1` and include
the console, settings helper, application wrapper and native hook library. This
is the bundle/helper/state contract, separate from WSJT-X UDP schemas 2 and 3.
Target trees are inspected as trusted root-owned data; their executables are
never run during preflight. Unknown/legacy protocols require pause and empty-route
audit. Compatible trees preserve outstanding ownership and any existing pause.
CI/release build checks compare `dxberryd -wsjtx-protocol` with the shipped marker.

An exclusive `native-release.lock` spans the updater's entire tree transition and
setup. Explicit reconciliation holds shared transition and legacy job-start
locks through completion, refuses active or unknown updater-job evidence, and
requires both an installed compatible bundle and a running daemon with the exact
installed console inode. A surviving daemon from the replaced tree cannot resume
collection. The daemon serializes pause/audit/resume with its lifecycle callbacks;
an offline pause can audit only with exclusive retained-state ownership. Resume
requires the running daemon and a fresh empty-route audit.

Root maintenance uses `dxberryd -wsjtx-release status` to inspect the current
receipt, then `dxberryd -wsjtx-release resume -wsjtx-release-receipt RECEIPT` for
explicit reconciliation after restoring a compatible installed bundle. A retired
receipt cannot clear a later pause. Repeating a completed receipt changes nothing.
The default state is `/var/lib/dxberry/station`; private hooks use
`/run/dxberry/station-programs`, while release coordination separately uses
`/run/dxberry`. Custom daemon `-program-release-run` and updater `DXB_RUN_DIR` must
match. The HTTP API exposes only `release_paused`; receipts and release actions
are root-only.

Container fixtures cover retained pause after refused/failed transitions, lock
scope through setup, stale receipts, old daemon identity, active updater jobs,
offline audit, acquisition refusal and recovery while paused. These are local
software checks, without real systemd transitions or WSJT-X. Real application,
systemd and workload/capture acceptance remain required before Pi activation.

The final local verification passed all-package Go vet/race checks, native
console/helper builds, bundle-marker agreement and ARM64 helper/test compilation.
All 906 shell regressions, ShellCheck, Bash syntax and build-tree checks passed.
Release-state fuzzing completed 354,042 executions in 20 seconds without failure.
The startup fixes separately passed 100 process-guard and 50 radio-update race
repetitions. The test helper disables only the race runtime's default exit sleep
to avoid adding a second to every private helper exchange.
The expanded standalone helper/controller/root-socket integration also passed
with Docker networking disabled. No remote CI job or Pi software test was run.

## Native status events

The daemon now connects `ProgramEvents` to the existing authenticated events
socket. Existing per-radio dashboard/page watches set aggregate interest for
both rig and program status; removing the last watch releases its cached native
state. Unknown radios are refused. Watching never initializes inventory, changes
consent, inspects hardware or starts a program. A default-off station returns its
disabled state without creating files or invoking the helper.

One producer refreshes up to 32 watched radios once per second, with a shared
500 ms read budget for the batch. A busy lifecycle lock produces an unavailable
view instead of preserving a live label. Periodic reads publish the tracker's
45-second expiry even with no incoming packets. Separate generation/epoch and
arrival timestamps remain intact. Source errors become fixed codes; neither
private errors nor a different radio's observation can cross the boundary.

The envelope is `type: program`, `program: wsjtx`, the radio, a server `sent_at`
timestamp and version-1 data. Data includes availability, collection state and
an explicit omitted-decode count. Collection state now exposes observed transport
counters and a failure boolean without socket/peer addresses or raw OS errors.
Program-reported TX remains an observation, with no added radio-control action.
Clients can compare server observation times with `sent_at` before using a local
elapsed-time clock; browser wall-clock agreement must not be assumed.

Each data snapshot is at most 256 KiB. If the full retained history does not fit,
the event contains a contiguous newest suffix of complete decodes and reports
how many were omitted. It never cuts a text field or changes the stored history.
An oversized base observation becomes unavailable. This is a replacement view,
not an append-only feed or a guarantee of lossless event delivery.

Subscriber queues hold one coalesced change signal, not copies of every decode
snapshot. Writers fetch the current cache, send only watched changes, and retain
only per-radio hashes to suppress repeats. Initial watches get a current view;
later notifications recover the latest state after coalescing. Every outgoing
frame rechecks login, including between frames in a batch. Existing write limits,
same-origin checks and connection cleanup still apply.

Container tests cover idle expiry through the actual producer loop, independent
radios, wrong-radio/error filtering, bounded escaped-text histories, detached
cache bytes, replaced watches, slow subscribers, default-off reads and cancelled
budgets. Real WebSocket checks cover initial snapshots, watch aggregation/removal,
unwatched clients, unchanged-state suppression and logout between batch frames.
Actual WSJT-X/systemd compatibility and CPU/capture acceptance remain before
deployment. The read-only display is recorded below. Nothing in this increment is activated on the Pi.

Thirteen new producer/socket tests passed with race detection, including the
logout-between-frames case repeated 25 times. All-package vet, the four affected
Go packages, native console/helper builds, protocol-marker agreement, ARM64
helper/test compilation and integration-script checks passed in Docker. The
expanded standalone integration also passed with networking disabled. After
adding the server send timestamp, status race checks, all-package vet and the
console build passed again. Other shell behavior is unchanged from the 906-test
release-guard verification above.

## Read-only native display

The dashboard's **WSJT-X status** link opens the assigned radio page directly at
its native panel, including when the program is stopped. The explicit fragment
scrolls and focuses the heading once; subsequent observations do not move the
page. The existing full program window remains available for operation.

The panel consumes versioned, radio-bound program events over the existing
socket. Refresh status only renews the watch. It never enables collection,
changes routing, starts a program, tunes a radio or sends UDP. Disabled collection,
maintenance pause, recovery, waiting, disconnected, closed, unsupported, loading
and unavailable states remain explicit. Losing the connection or reaching the
45-second protocol expiry clears readings and activity even without new frames.

WSJT-X Status is sent on changes, not at a guaranteed periodic interval
(`NetworkMessage.hpp`, Status documentation). Heartbeats establish protocol
liveness; they do not renew a Status report. While protocol updates continue,
frequency, mode and transmit state remain labeled **last reported**, with the
report's own increasing age. These values do not verify current RF output.
Decode times are UTC time of day without an invented date. Unknown values remain
unknown and decoded text is always a text node.

Activity starts with the newest 20 decodes and expands by 20 to the bounded
200-record session history. The table owns a fixed-height scroll region;
omitted history is explained. Existing light/dark styling is preserved, with
shared scrollbar tokens and a corrected light-theme emergency STOP foreground.
The durable design and behavior context is in root `DESIGN.md` and
`UX-CONTRACT.md`.

See [native display validation](wsjtx-native-display-validation.md) for exact
software evidence and outstanding verification. The fixture does not run the
actual WSJT-X application or establish physical device/browser acceptance.
No native collection, new console binary or routing change is installed on the Pi.

The separate idle-reporting maintenance increment later built the complete
development console and helper for ARM64. This closes the cross-build check,
not the actual application/systemd or workload acceptance gates. The Pi received
only the [isolated idle-reporting maintenance build](idle-reporting.md), based on
its prior console without native integration.

## Bounds and API

Implemented limits are 16 KiB per accepted datagram, 256 bytes for an
instance ID, 4 KiB per decoded text field, and 200 decodes per session. Reject
truncated/oversized datagrams and invalid lengths without refreshing liveness.
Use a bounded receive queue and expose dropped/malformed counters. These limits
are design choices to validate against installed-program traffic, not protocol
maximums.

The snapshot identifies radio, program, generation, schema, program version,
last accepted arrival and availability reason. A heartbeat's presence establishes
protocol liveness only. Mark stale state using monotonic elapsed time and a
version-verified heartbeat interval; clear current-state assertions after expiry.
Use nullable fields for unavailable readings. A newly discovered instance may
remain waiting for its first Status event; do not request Replay in this slice
or invent initial state from a heartbeat. No decoded messages are persisted
to flash by this adapter.

Expose snapshots and bounded updates through the existing authenticated,
same-origin station HTTP/events path. Do not add a LAN-facing UDP service or
accept a client-chosen host, port, shell command or protocol method. Keep UDP
parsing and program observation outside the radio-control critical path, and
measure CPU/capture behavior before deploying this added workload to the Pi.

## Implementation order and evidence

1. Verify the installed release's source/configuration and retain a small
   protocol inventory. Resolve destination coexistence without changing the
   current station.
2. Implement a bounded parser with real-layout byte fixtures, malformed/truncated
   lengths, null/empty strings, schema rejection and forward-compatible suffixes.
   Include float endianness, QTime sentinel, unknown types and confidence flags.
3. Implement session generation, expiry and bounded rings with a fake clock.
   Test two independent radios, identity conflicts, Clear, duplicates/replay,
   late retired packets, disconnect and restart.
4. Loopback reception with fixture senders is implemented. Complete the
   isolated running-WSJT-X compatibility check when the verified binary is
   available; fixture delivery does not establish that compatibility. The
   [acceptance gate and verified staging command](../../tests/integration/README.wsjtx-compatibility.md)
   record the pinned official package and remaining application/systemd evidence.
5. Authenticated read/configuration routes and bounded events are implemented
   locally. The read-only native display is connected with bounded history,
   explicit availability states and the full program-window link. Browser
   evidence checks that this workflow issues no radio or collection mutations.
6. Container checks precede a reversible Pi patch. Compare CPU, report latency
   and capture continuity with the existing station before enabling collection.

Physical browser/audio, two-radio operation and RF acceptance remain separate
from parser and loopback validation.

## References inspected

- [Existing WSJT-X module](../../provision/lib/apps/wsjtx.sh)
- [Program/session ownership](../../station/internal/session/owner.go)
- [WSJT-X protocol](https://github.com/WSJTX/wsjtx/blob/v3.0.2/Network/NetworkMessage.hpp)
  (inspected again at v3.0.2, blob `74a268fa9bb2b53af7407513aa531cd5c0bf5cf3`;
  the fixture record links the matching header and writer source).
- [3.0.2 configuration](https://github.com/WSJTX/wsjtx/blob/v3.0.2/Configuration.cpp),
  blob `bf598688ff91cebb5a763758f741c3538a716298`.
- [3.0.2 command-line options](https://github.com/WSJTX/wsjtx/blob/v3.0.2/main.cpp),
  blob `01aaf28e7c4919cfcaa4147e26f91821b3fcc137`.
- [3.0.2 profiles](https://github.com/WSJTX/wsjtx/blob/v3.0.2/MultiSettings.cpp),
  blob `55bad6f44d82398cde740751f9f798f8ec9f4bc2`.

## Metadata subprocess output correction

The shared program metadata buffer now uses a named `bytes.Buffer` field.
Embedding it promoted `ReadFrom`, allowing subprocess stdout copies to bypass
the bounded `Write` method. A finite 65,537-byte subprocess reproduced that
bypass; the fixed buffer refuses it and accepts the 65,536-byte boundary.
This corrects the buffer shared by the program-lifecycle and release-authority
readers. It does not close actual WSJT-X application or systemd acceptance.
Evidence is retained at `/workspace/dxberry-service-binding-tests/`.

## Environment constraint

The cloud development environment previously reported `vpn_enrollment_failed`
alongside HTTP 503 binary/package downloads despite an unrestricted ordinary
network policy. Actual official-package checks remain open until ordinary
downloads work. Pi access uses DockerPC independently of the development
environment's network enrollment. No direct VPN client or alternate routing
was installed in the executor, and the Pi is not a downloader or proxy.
