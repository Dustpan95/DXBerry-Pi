# Native program mutation leases

Status: development source only. The Pi retains its verified `.4` console and
maintenance patches. The production preset target source remains unavailable.

An entry generation change invalidates old evidence but does not exclude a
later reader while the entry's work continues. Native program mutation owners
now retain `programstate.Store.WithAdvanceLease` through local work and a fresh
postcheck. Clients still release their entry stamp before IPC; a server takes
its own lease. There is no caller-supplied skip or inherited root descriptor.

## Mutation boundaries and held-lock order

| Boundary | Locks retained through local mutation |
| --- | --- |
| Explicit native initialization | Existing native-release coordination, then generation exclusive lock through the fresh release-pause check, inventory creation and parent synchronization |
| Approval Enable | Generation exclusive lock before service/controller mutexes, through preview revalidation and approval publication |
| Configured start hook | Generation exclusive lock before service/controller mutexes, through route preparation/install or pre-start restoration |
| Release pause on the server | Generation exclusive lock before the service mutex, through pause publication and route audit |
| Offline release pause | After connection failure, a local generation exclusive lock through pause publication and offline audit; no IPC wait under the lease |
| Release resume mutation | External release coordination first, then generation exclusive lock and service mutex through fresh receipt check, audit and resume publication |

Unconfigured start keeps its existing no-op/refusal branch. Native stopped,
Disable, Recover, Close and emergency STOP do not acquire a generation lease.
Cleanup may still restore an owned profile or reduce consent while tracking is
unavailable. Existing service/controller serialization continues to order such
cleanup against a native operation already executing; no real-time bound is
claimed for underlying filesystem or child-process work.

No parent waiting for unit preparation or native IPC gains a retained generation
lock here. The start eligibility callback retains its existing read-only
assignment/presence check without taking the parent's record lock. An
unprivileged settings child receives no root generation-lock authority.

## Release resume and initialization races

Resume first performs its ordinary entry advance and briefly inspects the
receipt under the service mutex. An already completed exact receipt remains an
idempotent no-op. For a paused receipt, the service releases its mutex before
calling the trusted release-authority function. Neither a generation lease nor
the service mutex is held while acquiring that external coordination.

After authority is acquired, the service takes a mutation lease, reacquires its
mutex and reads the exact receipt again. A newer pause published during the
gap makes the old receipt fail. The external lease remains held through the
mutation and postcheck, including cancellation while work is still finishing.
It is released when local work completes. Failure never clears a newer pause.

Initialization likewise rechecks the release gate inside its mutation lease.
The earlier check precedes a potentially contended generation-lock wait; a
pause that appeared during that wait must refuse inventory creation.

## Verification and limits

Docker regression tests reproduced reader entry during unfinished approval/start
work and acquisition of release authority under the service mutex. With the
new boundaries, readers are refused throughout the work, including cancellation
when the simulated work deliberately ignores it. A release-pause fixture also
checks that its lease is acquired before waiting for the service mutex.

A resume-order fixture can acquire the service mutex and a generation read lease
from inside the authority callback, publishes a newer pause while authority is
pending, then verifies that the old resume refuses its retired receipt. Separate
resume/offline-audit fixtures keep readers out through delayed cancellation and
confirm that the pause remains. These authority/lifecycle callbacks are
simulated; they do not establish real package or manager acceptance.

A kernel-lock initialization fixture observes the writer's second descriptor
for the private lock inode, publishes a metadata-only pause during that wait,
then verifies that no inventory is created. Storage tests use actual separate
processes for reader/writer contention and recovery, and inject publication
sync failures before and after rename. Published generations are retained;
failed or uncertain publication never enters the mutation callback.

Evidence is retained at `/workspace/dxberry-native-lease-tests/`. No real program
workload, service manager, radio or Pi installation is part of these fixtures.

Full Go vet/race checks passed in Docker, as did a capability-free UID 65534
refusal check for both storage leases. Native and ARM64 builds produced all four
executables as local non-deployable fixtures. The newly built native metadata
helper passed all six actual-helper Docker fixtures. ARM64 tests and the
non-Linux library compiled; neither was executed. No shell source changed, so
the shell suite was not repeated. No hosted CI or release publication is claimed.

This covers the named invocation boundaries, not arbitrary direct calls to
component/settings primitives. Shell record/apply/session/provisioning/package
mutation phases still need compatible protection and lock-order/delegation
work. Cleanup is deliberately independent and must be accounted for by fresh
pending-job, process and restoration-journal evidence. Effective installed
unit/helper/module/profile binding also remains required. Consequently a read
lease and unchanged generation still grant no preset preview/recall authority.
