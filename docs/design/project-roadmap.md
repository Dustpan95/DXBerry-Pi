# DXBerry-Pi continuation roadmap

This is the working order for the carried-over punch list and feature ideas.
Completed work below has evidence in the continuation plan. Proposed features
are designs to develop, not claims about what the current image provides.

## Current baseline

The maintained Pi now runs `0.5.0-ci.review.19e4cf1`, from the source tree
integrated through PR #14. The complete helper bundle and generation tracking
are installed. Native collection and the experimental queue remain disabled.
See the [November 24 deployment record](station-deployment-2026-11-24.md) for
the source/build identities, verified configuration preservation and retained
rollback trees. Hardware and actual-application acceptance remain separate.

### Earlier maintenance history

The preceding rc4 installation carried recorded local fixes for Graywolf API wiring, Pat's
idle URL, provisioning writes, Ethernet startup, verified interface handoffs,
network-helper lifecycle, bounded radio-apply locking, and Qt settings conflict
preflight.
At that checkpoint the console ran `0.5.0-rc4+idle-reporting.4`: unused full-station reporting
pauses without suspending radio/audio/program activity. Its isolated maintenance
build preserves the installed Pat/audio fixes, stops service-health reads from
queuing full reports, and makes the emergency STOP label visible in both themes.
The latest [ownership maintenance](program-ownership.md) also rejects unverified
screen evidence, prevents a changed owner inheriting an old answer, and blocks
browser ownership during startup or after an unavailable program check.
The [audio lifecycle maintenance](browser-audio-lifecycle.md) frees quota for
lost peers using exact offer identity and retains the radio-poller startup fix.
Native WSJT-X collection remains disabled in the current coordinated bundle.
See the [installation and rollback record](idle-reporting.md).
The isolated [provisioning lock maintenance](provision-operation-lock.md#pi-maintenance-installation)
also serializes full provisioning runs and rechecks first-boot completion after
waiting. It was installed without running provisioning or restarting services;
radio/TX configuration, mixer and service identities were preserved. The
isolated patch did not install the tracking helper; the November 24 coordinated
deployment installed its matching helper and session condition.
The separate [update execution lock maintenance](update-execution-lock.md#pi-maintenance-installation)
is also installed: the command now retains its gate through job work, result
publication and exit cleanup. Its verified one-file update preserved the
console, all checked service identities and radio/TX/audio/network settings;
no update job or provisioning run was started during installation.
The [update result-binding maintenance](update-result-binding.md#pi-maintenance-installation)
now ties future job results to their admitted request and unit, keeps completion
unknown through execution cleanup, and rejects unrelated/legacy success data.
Its two-file patch preserved the existing lock helpers and `.4` service/radio
configuration; no job was run during installation.
The [settings execution lock maintenance](settings-execution-lock.md#pi-maintenance-installation)
is also installed through DockerPC. Its one-file replacement preserves confirm
and revert availability and retains the original command for rollback. Independent
diagnostics confirmed unchanged service identities, radio/TX/network files,
Graywolf configuration and mixer, with both PCM streams RUNNING.
The [settings result-binding maintenance](settings-result-binding.md#pi-maintenance-installation)
is installed as a separate command/library patch. Future results must match the
admitted request and unit, and cannot appear complete through execution cleanup.
Its installation preserved the same service, radio/TX, network and audio baseline
without running a job or restarting services.
The [radio publication maintenance](radio-publication-lock.md#pi-maintenance-installation)
is also installed: the shared save phase now excludes cooperating apply work
through record replacement/readback and cache update, closing before handover
service waits. Its one-library update preserves the `.4` console, service
identities and checked radio/TX/network/Graywolf/mixer/audio baseline.
The installed [pin metadata matcher](radio-pin-identity.md) checks recorded
USB IDs and serials when resolving presence and capturing an original ALSA
name. This rejects replacement metadata at the same port; full handover
revalidation and reconciliation remain open.
Its one-library installation preserved all seven service identities and the
unregistered manual radio/TX/audio setup without executing a radio operation.
The installed [presence checkpoints after service waits](radio-wait-recheck.md) refresh
USB metadata before claim wiring/success and cleanup routing. They retain
same-owner intent on a failed retry; complete handover revalidation and
reconciliation remain open.
Its single-library replacement preserved all seven service identities and the
manual radio/TX/audio setup without executing a handover or radio operation.
The [retained hotplug queue](radio-event-queue.md) is implemented experimentally
and remains off by default. It retains arrivals across busy/failed apply work;
actual udev-device delivery and whole-tree update/rollback integration must pass before
activation. The new queue is not running on the Pi.
The isolated real-manager core delivery checks passed in CI; they do not close
the remaining device, retirement or recovery gates.
Its verified candidate payload is staged privately on the Pi; live runtime
files, service identities and radio/TX/audio configuration were preserved.
The subsequent [queue/update barriers](radio-event-update-gates.md) exclude
experimental worker/publication activity during a public update and refuse a
tree swap while queue configuration, retained work/history or unknown manager
state remains. Automatic consumer retirement and recovery remain open.
The isolated update-library refusal guard is installed, with its original
retained and the seven-service/radio/TX/audio baseline verified. The matching
protocol-2 queue payload is staged privately and remains inactive.
The subsequent [publication preflight](radio-event-publication-preflight.md)
requires the shared coordination functions and refuses experimental configuration
writes when the helper is missing, incompatible, unsafe or changes during its
bounded query. Complete deployment binding and real manager acceptance remain open.
Its verified eight-file candidate is staged privately on the Pi. Live files,
the installed safeguard, all seven service identities and the radio/TX/audio
baseline were preserved; the queue remains inactive.
The installed [update freshness maintenance](update-freshness.md#pi-maintenance-installation) prevents failed repository
refreshes and old cache records from presenting stale package availability as
up to date. The subsequent [system maintenance](system-maintenance-2026-11-23.md)
upgraded 33 reviewed packages with retained originals and configuration/database
backups. Independent checks preserved the four core station service identities,
radio/TX settings and running audio streams; web/database services restarted for
their updates. The subsequent [EEPROM tools package maintenance](eeprom-package-maintenance.md)
upgraded `rpi-eeprom` with its updater restart blocked, retained the original
package and boot/package-state backup, and preserved all eight service identities
and boot files. No firmware update or reboot was performed. The five deferred
packages received [authenticated original archives](system-package-restoration-archives.md)
and were then [upgraded with fresh restoration backups](system-package-maintenance-final-2026-11-23.md).
Independent checks preserved all eight service identities, boot files, protected
configuration, radio/TX settings and running audio streams. A fresh version-2
availability check reports zero ordinary system upgrades. A separate WSJT-X
backports revision remains kept back by APT. Actual firmware/boot acceptance remains open.
The installed [system upgrade preparation](update-worker-preparation.md) patch
also requires a strict repository refresh and successful simulation before
upgrading. Its one-library replacement preserved all seven service identities
and the radio/TX/audio baseline without running an upgrade job.
The manual boot-scoped capture report is also installed without changing normal
polling. The [capture investigation](../diagnostics/graywolf-capture.md)
identifies an upstream CPAL/Graywolf recovery migration that is not yet in a
newer published Graywolf release; it remains a candidate requiring verification,
not an installed fix or a closed audio acceptance item.
The November 23 follow-up confirms additional pre-update capture error bursts:
its truncated latest 1,000 input records occupy three short logged clusters.
Those records are neither boot totals nor measured audio gaps. The `.3`/`.4`
updates do not change modem recovery, and capture acceptance remains open.
Receive was confirmed
with an on-frequency packet. RF transmit was restored after the owner repaired
the radio. Preserve those settings while continuing development.

The available acceptance equipment is the current radio and phone/computer.
The FT-991 and its USB interface are deferred at the owner's request. The
current CODEC is used by a manually configured Graywolf channel, with no pinned
DXBerry radio. Adding it as a radio can rename ALSA devices; that requires a
planned transition and restoration, not an incidental test setup step.

## Finish the current station first

| Work | Evidence already available | Remaining evidence |
| --- | --- | --- |
| Radio receive | Known on-frequency Mic-E packet decoded; later receive count increased | Assess capture errors independently of sparse rural traffic |
| Browser audio/PTT | Ten real-Chromium checks, including STOP contrast, using production HTTP, ownership, PTT and WebRTC with simulated hardware | Actual phone/desktop audio, Safari, routing, latency and physical RF behavior |
| Network reliability | Failure/retry tests, helper-preserving real Pi restarts | Physical cable failover and cold boot |
| Program windows/logbook | Existing software tests and installed applications | Owner's real application workflow and contact; preserve accounts/database |
| Multi-radio/CAT/tone/SWR | Software guards and simulated radio checks | Two-radio and matching hardware tests; FT-991 deferred |
| Install/update/rollback | In-place maintenance with retained backups | Spare-media first boot and an agreed hardware rollback exercise |
| Release | Development commits and isolated tests | Hardware acceptance and explicit approval for a stable tag |

Do not interpret a quiet APRS interval as a failed receiver. Do not use fixture
PTT, a passing software guard, or service health as evidence of physical keying,
audio quality, SWR protection or an acceptable delay.

The first next hardware step is receive audio on the current radio and browser.
Identify the radio model and browser, snapshot the working manual setup, and
agree on any temporary radio registration before changing it. Transmit acceptance
is a separate step; no test transmission is implied by restoring normal operation.

## Reconciled older software findings

| Archived finding | Current disposition / evidence |
| --- | --- |
| TX loopback races and startup clearing | Existing per-PTT audio serialization; startup clear precedes HTTP serving (`rig/ptt.go`, `cmd/dxberryd/main.go`) |
| Shutdown while PTT opens | Existing shutdown/in-flight keying guards and unkey tests in `rig/ptt_test.go` |
| Audio status ignores caller deadline | Existing bounded caller deadline in `cli/cli.go`, covered by `TestCLIHonoursAnEarlierCallerDeadline` |
| Rollback leaves audio servers running | Existing cleanup when the restored tree lacks `dxberry-audio` in `provision/lib/update.sh` |
| OnClosed subscription unproven | Separate close/loss fakes and `TestPTTDropsWhenTheAudioConnectionCloses`; real-browser audio close also unkeys |
| Unknown screen state or changed program owner | [Ownership evidence hardening](program-ownership.md): exact session/state checks, successful manager reads and per-owner cached evidence; Docker regressions cover claim/takeover refusal |
| ATU window bypasses station guard | Existing `TestPollerHooksGuardTheStationsKeyingInsideAnATUWindow` |
| Page audio/mic/level findings | Current exception-safe TX updates, deliberate-stop filtering and float sample metering; physical threshold still needs calibration |
| Failed offer retains audio slot | Fix in this continuation: release the slot and peer on failure; regressions retain the failed socket, fill all healthy slots, and retry |
| Provisioning writers, startup carrier, apply lock | Fixed and installed; see continuation plan and Pi patch manifests |

The [browser audio lifecycle fix](browser-audio-lifecycle.md) now releases a
lost peer's quota without relying on the page closing its signaling socket.
Per-offer identities reject delayed loss notices for replacements, and per-tab
serialization preserves reconnect ordering. Docker regressions reproduce the
old refusal and verify pending-offer quota, stale notices and overlapping
reconnects. The per-login cap remains four. This fix and the existing poller
startup-order fix are retained in the isolated `.4` maintenance console.

## Next feature: native program status, then controls

Start with a read-only WSJT-X adapter; the focused
[adapter design](wsjtx-status-adapter.md) records the protocol, lifecycle,
configuration conflicts and implementation checks. Keep the existing full program window
available. Show the owning radio/program, running/disconnected state, mode,
frequency and a bounded stream of decodes supported by the installed version.
An unavailable or old program must produce a clear unavailable state rather
than an invented zero value or a stale "connected" label.

The local backend now parses verified Qt schemas 2/3, tracks separate radio
sessions and bounded decode history, and receives only on private loopback
endpoints. Durable endpoint retirement and a routing journal preserve ownership
across restarts. Explicit per-radio consent binds to a fresh settings preview;
the application-user helper installs/restores only at verified systemd lifecycle
boundaries. Root unit hooks, private daemon IPC and authenticated read/configuration
routes are connected. These foundations are in the coordinated bundle, with
collection disabled on the Pi.

An update/rollback guard now pauses new acquisitions before incompatible tree
changes and proves all managed routes restored. Failed transitions retain the
pause outside the release trees. Explicit reconciliation requires the current
receipt, an installed compatible bundle, the matching running daemon and no
updater in progress. Compatible publishers declare bundle protocol 1 and preserve
the latest pause, endpoint inventory, journal and approvals. The helper and marker
are included in release artifact paths. See the adapter design for root recovery
requirements. The guard is included in the coordinated Pi bundle; actual
application-routing and incompatible-release recovery acceptance remain open.

Container checks cover real Qt serialization, malformed-input fuzzing, concurrent
sessions, standalone cross-user helper transactions, crash recovery, root socket
credentials, authenticated HTTP routes and update/rollback failure handling.
Systemd lifecycle state is a fixture. Bounded native status events now share the
authenticated radio watches, publish idle expiry and retain only the latest
snapshot for slow subscribers. A single producer has a shared read budget and
bounded complete decode history; default-off reads never initialize collection.
The read-only native display now shows last-reported frequency/mode/transmit
state and bounded band activity, with explicit loading/offline/stale/recovery
states. A dashboard link opens directly at the panel; the full program window
remains available. Container browser checks verify history/keyboard/phone/theme
behavior and zero radio or collection mutation intents. See the
[native display validation](wsjtx-native-display-validation.md) for limits.
Actual WSJT-X 3.0.2 compatibility and CPU/capture workload acceptance remain;
required design-format lint is blocked by the environment's VPN enrollment
failure, and the static UI audit retains documented legacy findings.
The [running-program acceptance gate](../../tests/integration/README.wsjtx-compatibility.md)
now records the independently rechecked upstream release digest, a package staging
command and the specific application/systemd evidence still needed. Its offline
download-failure checks passed; successful staging remains blocked by connectivity.
A startup race found during validation is also fixed locally: the radio poller
subscribes before its first update can be published, and program ownership checks
refuse an application process with temporarily unobservable arguments during exec.

Configuration preflight previously fixed a shared INI-writer defect: raw
read-back could falsely verify a value when Qt selected a duplicate or alias.
That tested helper is installed as a library-only patch (`034ee5a`), with no
service restart or change to program/radio settings. It is separate from the
native backend and release guard awaiting deployment acceptance.

The current N1MM-compatible contact bridge on UDP 2333 is for logging, not a
native program-control endpoint. Verify each installed program's protocol and
configuration before assigning a separate loopback endpoint per radio. Preserve
operator preferences and existing logging; never let two radio instances share
an ambiguous control identity.

Proposed backend contract:

- Per-radio adapter identity includes program, session generation and protocol
  version. Late events from a stopped/replaced session are discarded.
- Browser requests pass through station authentication and same-origin checks.
  Program network sockets stay on loopback; no new public program API port.
- Reads use bounded timeouts and output sizes. Decode history is an in-memory
  ring, not an unbounded flash log. Lost data and reconnects are explicit.
- No generic network-method proxy or arbitrary shell-command field. Add named
  capabilities one at a time, with malformed/oversized packet coverage.
- Phase one sends no transmit, tune, macro or mode-change commands. Controls
  need explicit ownership checks and verified stop/unkey behavior first.

After WSJT-X status works, add JS8Call's version-verified JSON API, fldigi's
allowlisted XML-RPC reads, and Pat's supported HTTP status. Each adapter must
handle absent programs, restarts, incompatible versions and two independent radio
sessions. A fake protocol server proves parsing; the installed program proves
compatibility. Physical RF remains a separate acceptance step.

An independent [Pat status client](pat-status-adapter.md) is now implemented
locally against the reviewed 1.0.1 `GET /api/status` contract. It has a fixed
loopback destination, bounded reads, no redirects or control operations, and
projects only connection/listener state. A default-off development flag now
wires a named read-only HTTP endpoint through authentication and same-origin
checks, without a native panel, polling or activation on the Pi. A dedicated Linux
reader checks the sole fresh assignment,
reviewed package version, unit invocation, actual process and owned listener on
both sides of each HTTP read, discarding replacements and file rewrites.
Docker fixtures, real proc/socket metadata checks, vet/race suites and ARM64
compilation passed. Actual running-Pat/systemd compatibility, workload acceptance
and native display remain before station activation. The pinned
x86_64 fixture download is still blocked by HTTP 503; the cached runtime also
cannot execute the Pi's ARM64 program as an alternative.
An [idle-only application runner](../../tests/integration/README.pat-compatibility.md)
is now prepared for that official package. Its Docker isolation and invalid-input
failure path are checked; its actual application success path remains unverified.
It cannot close the separate real systemd, connected-session or workload gates.

## Follow-on slices

The installed `.4` ownership maintenance closes cached-free answers after probe
failure, startup uncertainty and out-of-order refresh publication. It also keeps
STOP available when program state cannot be verified. This is an isolated
follow-up to the `.3` console; the preset target still needs fresh
program/lifecycle authority. See [program ownership](program-ownership.md).

The first [idle reporting](idle-reporting.md) slice is implemented: optional
full-station reports stop when no events clients remain, resume when observed,
and remain available on demand. Radio control, guards, active audio and running
programs keep their independent schedules. This reduces unused reporting work;
it does not establish that the intermittent capture fault is fixed. The
maintenance candidate is based on the installed console plus this scheduler
change, keeping the unaccepted native WSJT-X integration out of that deployment.

Tracing preset target qualification also exposed a [rigctld pin-binding and
restart-retry problem](rigctld-binding.md). The development shell fix now makes
used-pin changes affect its desired environment and retains rejected restart
intent. Docker shell regressions and the full suite passed. It is source-only;
queued work is not actual process/device acceptance, and the preview verifier
remains an open gate. The Pi's manual Graywolf arrangement was not changed.

The [managed connection foundation](frequency-presets.md) now binds an existing
poller and radio record to its actual Linux TCP socket metadata and rejects a
different configured peer. Docker verification also exposed and fixed a poller
constructor/state-callback race. The connection anchor does not establish peer
process, effective device configuration or program-free authority; production
preview remains disabled pending those checks. This work is not installed on
the Pi.

The next read-only Linux check binds that exact connection to an expected
process's listener and accepted socket, with repeated kernel and process
identity reads. Service/configuration/device and program-free checks remain
necessary before enabling production preview.

A subsequent service reader now checks that peer against the exact running
unit, invocation, root-owned executable and process/cgroup identity. Its
production paths are fixed and it issues only metadata queries. Effective
arguments now have a separate registration-to-command comparison, checked
against the existing provisioning function. Actual opened devices and
program-free authority still block production preview activation. A subsequent
[native USB descriptor reader](rig-device-binding.md) now supplies a read-only
software foundation for the device check; live USB/service acceptance and
flrig/GPIO support remain open.

The [radio lock guards](radio-locks.md) also refuse changes when
locking is unavailable instead of silently continuing. They preserve existing
lock scope and keep emergency PTT STOP separate. The four-script maintenance
patch is installed on the Pi without restarting any service; the console stays
on `.4`. They do not yet establish continuous program lifecycle authority for presets.

The [settings and update lock guards](settings-locks.md) now also require their
existing command-start locks. The isolated two-script patch is installed on the
Pi with verified retained originals and unchanged service identities, radio/TX
configuration and mixer. Asynchronous jobs still need separate coverage before
these locks can contribute to a complete preset recall exclusion protocol.

A [durable generation store and root helper](program-generation.md) now provide
atomic storage and a bounded process interface for future activation tracking.
Native/ARM64 packaging and actual-helper process checks passed in Docker. The
source session unit now advances the generation before its presence check or
preparation, independently of cleanup. Complete writer coverage, module
classification, fresh unit evidence and the recall exclusion boundary remain
open; this feature is not installed on the Pi or connected to preset permissions.

[Radio mutation generation guards](radio-generation.md) now cover validated
publication and the earlier handover actions. Failed compensating release is
reported as unconfirmed, with cleanup still attempted. Apply/hotplug now also
advance before derived work under the apply lock, with actual-helper and runtime
mirror fixtures. The parent-directory rule now runs in radio setup before its
first apply. [Package mutation guards](package-generation.md) additionally cover
install/removal and update-job entry points, before native preflight or live
program work. [Valid full provisioning](provision-generation.md) now prepares
tracking and advances before configured steps, with no untracked fallback.
[Session/native guards](session-generation.md) additionally invalidate before
session start/preparation and native acquisition/release maintenance, with
cleanup kept independent. Direct component primitives, whole-job exclusion and
the fresh target verifier remain open; these changes are source only.

The [shared generation read lease](program-generation.md#shared-read-lease)
now keeps cooperating advances out of a synchronous callback and its postcheck,
even after caller cancellation. Docker process tests cover contention and
recovery. It remains separate from preset authority: writers already in flight,
mutation-phase lock ordering and the installed-protocol/fresh-free verifier
still require integration. Nothing from this extension is installed on the Pi.

[Native mutation leases](native-mutation-leases.md) now close that interval for
the named native server, offline-pause and initialization entries. Resume
acquires external authority outside service/generation locks and rechecks the
receipt after waiting. Shell mutation phases, installed binding and the fresh
free-state verifier remain open; this extension is also source only.

A [full provisioning operation lock](provision-operation-lock.md) now remains
held through valid configured runs, with no tracking/live work on contention
and a first-boot completion recheck after waiting. Unit/native hooks do not take
this parent lock. Update/settings/record/apply phase coverage and the complete
installed-protocol/free-state verifier remain required. The separate isolated
maintenance installation is recorded in that design note; it adds no tracking
helper or production preset authority.

The [update execution gate](update-execution-lock.md) additionally covers the
public apply command through body, result publication and EXIT recovery, while
keeping job admission and unit/provisioning hooks on separate locks. Settings
execution, complete caller/installed binding and fresh job/process evidence
remain open. [Update result presentation](update-result-binding.md) now binds
completion to a unique admitted request and matching unit, rejects legacy or
unrelated results, and refuses completion while execution cleanup holds its gate.
Actual manager/invocation evidence remains separate from these documents.

The [settings execution gate](settings-execution-lock.md) adds the corresponding
full-worker boundary through provisioning, follow-up, timer recheck and result
publication. Confirm and revert retain their existing short settings lock.
[Settings result binding](settings-result-binding.md) now identifies admitted
requests before launch and rejects unrelated completion while retaining recovery
availability. Actual recovery/workload acceptance and remaining caller/installed/
free-state evidence remain open.

The [radio mutation-phase audit](radio-mutation-phases.md) identifies overlapping
handover/apply callers and a dependency cycle in a rejected whole-handover lock.
A Docker regression now protects prerequisite hotplug progress during a session
claim. The next exclusion work must separate publication from service waits,
retain visible pending intent and reconcile contended hotplug events; this audit
does not change the Pi or enable preset authority.

The next [radio publication phase](radio-publication-lock.md) now shares apply
exclusion only through generation, record replacement/readback and cache update.
It releases the gate before a handover can wait for a service. Whole-handover
exclusion, pending intent/reconciliation and fresh post-wait authority remain open.

| Idea | First useful slice | Boundary before expanding |
| --- | --- | --- |
| Presets/profiles | [Frequency preset storage](frequency-presets.md): development storage/configuration transport, injected preview/recheck service and authenticated HTTP adapter implemented; production target verifier, UI and guarded recall/undo remain | No profile apply during transmit; preserve unrelated program configuration |
| Macros | Stored text templates scoped to a program/radio | No arbitrary shell execution or automatic RF send; controls require ownership/stop semantics |
| Band conditions | Timestamped, attributed conditions with cached/offline state | Select a data source and policy; do not infer local RF conditions from remote forecasts |
| VPN pairing/guest | Explain access through the user's existing VPN and station login | Keep LAN/user-VPN architecture; guest permissions require a separate role/session design |
| Idle mode | Stop optional polling/UI work while idle | Never suspend an owned transmitter, active audio or a running digital-mode session |
| SDR scope | Identify hardware and a bounded read-only spectrum transport | Do not assume the current USB audio CODEC supplies IQ data; measure Pi CPU/network load |
| CW keyer | Choose a supported hardware/backend timing interface | Do not time physical keying with browser timers; require stop/watchdog and hardware acceptance |
| Overlay root | Separate read-only system and retained state/update workflow | Inventory database, mail, logs and update writes; prove recovery on spare media |
| Hotspot fallback | Explicit recovery state with saved network config | Needs local recovery access; keep one configured address holder and test return to LAN |
| Pi 5 | Separate image/build target using supported boot/kernel inputs | A Pi 4 build does not establish Pi 5 boot, USB, thermals or audio behavior |
| Offline image | Dependency inventory, checksummed payloads and version manifest | Distinguish offline first boot from online updates; account for size and redistribution terms |
| Network editing | Preview/apply/reconnect/keep/undo state machine | Keep `DXB_CONFIG_NETWORK=0` until new-address confirmation and recovery are hardware-proven |

This order avoids coupling unrelated ideas into one large station update. Every
slice needs a small design, observable failure states, a rollback path where it
changes persistent state, and evidence appropriate to the behavior being added.

## Decisions still open

Preserve current behavior while awaiting answers: Tune is FM/AM only; dashboard
Listen opens the radio page; the diagnostic journal is capped at 64 MB; external
logbooks require verified HTTPS except the existing local Wavelog integration.
Optional LAN HTTP or self-signed external logbook support requires a deliberate
transport policy and clear per-server settings.

Stable tags, upstream messages and destructive housekeeping remain separate
decisions. No new image, stable release, external report, backup deletion or
history rewrite follows from this roadmap.

## Evidence locations

- Installed patch manifests: `/var/lib/dxberry/local-patches/` on the Pi; the
  local continuation record retains detailed test and installation evidence.
- [Station architecture](2026-10-25-station-app.md)
- [Applications and ownership](2026-10-19-applications.md)
- [Radio plumbing](2026-09-09-radio-plumbing.md)
- [Browser test scope](../../tests/integration/README.browser.md)
