# Radio mutation phases and service ordering

Status: audited caller boundaries and a regression fixture. The overlapping
handover/apply scope remains open. No runtime change or Pi installation follows
from this audit; production preset authority remains unavailable.

The subsequent [publication exclusion](radio-publication-lock.md) adds apply 9
only around the shared validated save phase. It closes before handover service
waits; the broader caller gaps and ordering requirements below remain open.

The later [pin metadata matcher](radio-pin-identity.md) checks recorded USB
identity fields during presence resolution. That improves the initial check;
it does not refresh evidence after waits or supply the pending/reconciliation
protocol described below.

The later [presence checkpoints](radio-wait-recheck.md) refresh scanner metadata
at named claim and cleanup boundaries. Source/invocation stability, device
continuity, pending intent and reconciliation still require the full protocol.

The experimental [event queue](radio-event-queue.md) retains hotplug requests
across overlapping or failed attempts. It remains off by default and does not
represent handover pending intent or complete reconciliation acceptance.

## Current caller scopes

These are the audited public/coordinator boundaries, updated for the later short
publication phase. They do not prove that every direct component call
participates in one protocol.

| Entry | Retained locks | Work and limitation |
| --- | --- | --- |
| Radio add/set | Record 8; separate apply-9 publication and derived phases | Publication releases 9 before later apply reacquires it; a failed apply may follow a saved record. |
| Radio remove | Record 8; separate apply-9 publication and derived phases | Owner release precedes apply. Card restoration follows apply after 9 closes. |
| Radio claim/release | Record 8; apply 9 only during shared saves | Stop/unwire, routing and service waits remain outside the short publication gate. |
| Standalone full/hotplug apply | Apply 9 | Loads desired state and changes derived files/services; does not acquire record 8. |
| Program start/stop | Record 8 | Starts may wait for session activation. Ordinary stop keeps its existing record dependency; emergency PTT STOP is separate. |
| Program removal worker | Update execution gate and record 8 | Rechecks ownership, stops/disables sessions and removes packages/files. It does not hold apply 9. |
| Full configured provisioning | Provisioning execution gate; apply 9 only inside radio apply | Package/unit/GPS work occurs before radio apply. Direct substeps are not covered by the whole-driver gate. |
| Session present/prepare/stopped hooks | Neither parent record nor apply lock | A waiting parent can already hold those locks. Preparation has generation guards; stopped cleanup remains independent. |
| Native local mutation | Native coordination and generation mutation lease at named entries | Separate from record/apply scopes; client entry releases its generation lease before IPC. |

Update/settings execution gates cover their named full workers, including
result and EXIT cleanup. They do not exclude an independent radio claim or
hotplug invocation. Generation invalidation records a change but does not hold
off later work after the advance helper returns. Cached holder state, a free
lock, or an ended job document is insufficient preset authority.

## Why a whole-handover apply lock is unsafe

Claims and standalone hotplug can overlap outside record publication because
those larger scopes hold different locks.
A prototype extended apply exclusion from the radio CLI's record load through
claim/release and final cleanup. It passed four command/filesystem concurrency
groups: refusal before record load, all dispatch phases under exclusion, a
delayed handover excluding hotplug, and callback state/exit/descriptor behavior.
All four groups failed on the baseline as expected.

The prototype nevertheless introduced a dependency cycle. The actual
`dxberry-screen@.service` is ordered after `dxberry-radio-hotplug.service`.
`dxb_app_session_start` waits for that session to become active. A queued hotplug
process needs apply 9. Holding 9 through the claim's wait can prevent that
prerequisite from completing, leaving the session unable to start until the
claim times out and releases its lock. `rigctld@` and ARDOP also have hotplug
ordering; the hotplug unit is ordered before Graywolf. Their wait and restart
paths require review too.

```mermaid
flowchart LR
    C[Claim holds apply 9] --> S[Wait for session activation]
    S --> H[Session ordered after hotplug]
    H --> L[Hotplug waits for apply 9]
    L --> C
```

The existing rigctld synchronization and apply-driven session restarts already
queue starts without waiting for this reason. Extending the lock over existing
synchronous handover waits would defeat that separation. Unit hooks must not
gain their parent's locks, and cleanup/unkey must not depend on successful
tracking or target verification.

## Regression evidence

`test_radio_lock_order_claim_leaves_hotplug_prerequisite_available` reaches the
real session-start wait through the actual radio CLI. Its explicit manager
model permits activation only when the prerequisite can finish the real apply
wrapper. Derived device/service work is a marker; the kernel flock is real.
The test passes on the unchanged runtime source and fails on the rejected
whole-handover prototype (claim exit 5, no completed prerequisite).

The zero-second fixture wait makes this deterministic and immediate. It is not
a measured Pi timeout or real systemd workload acceptance. The earlier four
passing prototype groups alone did not establish safe lock ordering. Their
sources, rejected patch, hashes and both sets of Docker logs are retained at
`/workspace/dxberry-radio-change-tests/`. The prototype was removed from the
working runtime files and was neither committed nor installed.

The final focused regression, repository Bash syntax and ShellCheck passed in
Docker. The tree check passed on a non-deployable tracked-source snapshot with
executable zero-byte binary placeholders, as used by the shell checks. Its first
attempt against the checkout lacked packaged preset/helper binaries; no binary
was built or installed to satisfy that packaging-only check. Full shell/Go/
browser suites were not repeated for this audit/test-only change.

Read-only DockerPC diagnostics independently verified the existing settings
patch and saved originals, unchanged identities of all five services, checked
radio/TX/helper/network files, Graywolf configuration and mixer. Both PCM streams
remained RUNNING. The Pi remains on `.4` and its verified settings result-binding
maintenance; no radio command, job, configuration change or restart was issued.

## Required next boundary

Separate synchronous record/derived-file publication from service queuing,
dependency-sensitive waits and follow-up work. For every phase, document the
fresh input, retained locks and generation evidence, the operation that can
wait, and the mandatory recheck after waiting. Never retain a parent lock or
generation write lease across a unit/IPC dependency that may need it.

A pending operation must remain visible while locks are deliberately released.
Later phases must reacquire their locks and revalidate desired owner, source,
device, invocation and restoration state. Hotplug events need reconciliation
after contention; simply dropping a busy hotplug event or treating it as success
would introduce another gap. Direct unit hooks and surviving descendants remain
part of the writer audit. No inherited descriptor or caller-controlled bypass
may stand in for that protocol.

Implement and test those phase boundaries before constructing a production
free-state verifier. Hardware/manager acceptance, trusted reader paths, installed
unit/helper/module/profile binding and complete lock ordering remain open.
