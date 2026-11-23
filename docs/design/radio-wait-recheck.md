# Radio presence after service waits

Status: implemented and installed as isolated Pi maintenance. This adds named
presence checkpoints, not complete handover authority.

The initial claim check used a cached scan. Startup/readiness, old-owner
shutdown/settling and wiring-triggered restarts can outlive a USB removal or
replacement. A claim could therefore wire using stale presence or report
success after its device disappeared. Failure/release cleanup could also
restart a sound server using the pre-wait answer.

## Named checkpoints

Claims now refresh scanner metadata and use the installed
[recorded-pin matcher](radio-pin-identity.md):

- After the old owner lets go, including its settling wait before a session
  claim publishes its new owner or changes routing.
- After shared-owner startup/readiness, before wiring, and after wiring returns
  because wiring can restart the owner's service.
- After session startup/activation, before reporting success.
- At the same startup/wiring boundaries for a retry of an existing owner.

A lost/mismatched device prevents a success answer or new wiring mark. A new
claim uses the existing stop/unwire and compensating-release path, returning 5
when release succeeds or 8 if it cannot confirm the release. Same-owner retries
return 4 on lost presence while retaining their desired owner; they do not
declare that the radio is free. A loss before a new owner is published returns
4, possibly after the old owner has already been released.

Startup can fail before the normal presence checkpoint executes. Cleanup
therefore refreshes again before synchronizing the unowned sound server or
session-failure rigctld routing. Release also refreshes after old-owner
settling. Missing/changed metadata yields absent presence, preventing those
paths from starting a sound server or rigctld for the lost target. Cleanup and
unkey are not conditional on a successful presence/tracking check.

Existing caller record serialization is retained. No apply or generation write
lease is added across service waits, and unit hooks do not gain their parent's
locks. The hotplug prerequisite ordering remains unchanged.

## Limits

These are fresh metadata checks against the caller's existing desired record.
They do not prove device continuity through an unplug/replug, unique identity
for indistinguishable devices, source/invocation stability, opened descriptors
or old-owner quiescence. Scan-to-use races, apply-driven rewire waits, direct
writers, pending-operation visibility and hotplug reconciliation remain open
in the [handover phase work](radio-mutation-phases.md). A same-owner retry may
have started or partly wired a program before a later failure; retained desired
state and older wiring marks are not evidence of completed handover. Production
preset authority remains unavailable.

## Verification

Seven Docker regression groups change synthetic sysfs at explicit service
boundaries. They cover new shared claims after readiness/wiring, existing-owner
retries, new/existing session activation, old-owner shutdown/settling, failed
shared/session startup cleanup, release cleanup and matching-device kernel
renumbering. All seven fail against the previous functions. The first patched
run passed all seven; the final tests also exercise failed session startup
before its ordinary post-start checkpoint can run.

These fixtures execute real claim/release functions with simulated services and
synthetic USB metadata. They do not establish real manager, program-workload,
device, RF or audio-quality acceptance. Evidence is retained at
`/workspace/dxberry-radio-wait-tests/`.

The final focused maintenance run passed all seven groups, including failed
session startup. Both full Docker snapshots then passed: 1,014 development
and 926 isolated maintenance shell tests, zero failures, followed by Bash
syntax, ShellCheck and tree checks. The existing hotplug prerequisite-ordering
regression passed in both. Installer fixtures passed atomic replacement,
complete rollback and retained diagnostics on incomplete rollback. Exact
baseline checks preserve the installed feature exclusions and all existing
maintenance fixtures. No production Go or UI changes required their broader
suites to be repeated.

## Pi maintenance installation

Installed through DockerPC at `2026-11-23T15:48:53Z`, from source
`43cad0f7a77a5d8e74a02399e62a4a452c860cb8`. The only replacement is
`/opt/dxberry/lib/radio.sh`, root-owned mode 0644:

| Previous SHA-256 | Installed SHA-256 |
| --- | --- |
| `b0eb3110558b7f3eacf107cd02ba99acc676c8684261d5769aedb46688bc683d` | `dfc3ecd311dc74dc1020683bc6d2160e82f7beab993a262d269237d18ca589bc` |

The previous library is retained at `/root/dxb-radio-wait-43cad0f`; staging
remains at `/root/dxb-radio-wait-stage`. The installation record is
`/var/lib/dxberry/local-patches/radio-wait-recheck.json`. This supersedes the
radio-library hash in the pin metadata record, preserving the matcher,
publication gate and all other local maintenance.

The isolated candidate replaces only the three claim/session/release functions
and adds their fixed presence helper on the exact installed baseline. Existing
development generation hooks remain excluded; no tracking helper or epoch was
installed. All eight transfer pieces and the 22,986-byte archive were verified
(SHA-256 `af5a30757813ffe63ff5deebde9b2cbce36e338ab28b155ab5cabdce934f583f`).
The installer checked the old library/console and absence of active/pending
jobs, retained existing operation gates, saved and fsynced the original, and
used atomic replacement with rollback on post-check failure.

Separate read-only diagnostics verified installed/original hashes and mode,
all seven unchanged service identities, the `.4` console, checked radio/TX/
helper/network files, Graywolf configuration and mixer. Both PCM streams stayed
RUNNING; installer HTTP and verified HTTPS version checks passed. The manual
Graywolf/CODEC setup remains unregistered. No claim/release, radio registration,
record save/apply, job, provisioning, package upgrade or service restart was
performed during this installation. Those diagnostics establish preservation
of the working baseline, not actual handover or physical acceptance.
