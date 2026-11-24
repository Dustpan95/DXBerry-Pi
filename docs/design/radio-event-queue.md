# Retained radio hotplug requests

Status: experimental implementation, off by default. It is not activated on the
Pi. Real manager delivery and whole-tree update/rollback integration remain
required before production activation.

The current udev producer directly starts a oneshot hotplug unit. Another start
while that unit is active can be coalesced without retaining a request for a
later scan. This work separates quick event retention from the apply attempt.

## Producer and worker

`dxberry-radio-event enqueue` records a root-owned empty `pending` directory
under `/run/dxberry/radio-events/marks`. Repeated arrivals coalesce into that
bit. It performs no manager call, device operation or apply/record/worker lock
acquisition. The runtime directory is checked for root ownership and safe modes;
private queue directories require mode 0700. No event payload or radio selector
is accepted. The configured runtime directory and private queue directories are
checked; this is not a descriptor-bound traversal of every filesystem ancestor.

The worker serializes itself on a private empty, single-link mode-0600 lock
outside `marks`. It rejects links, unexpected entries and nonempty marker
directories. Under that independent worker gate it atomically renames
`pending` to `processing` before invoking the existing `dxberry-radio hotplug`
command. The child does not inherit the worker lock descriptor or an alias.

An event arriving during apply creates another `pending` marker. A successful
invocation acknowledges only its unchanged `processing` marker. Later arrivals
remain pending. Failure or interruption retains processing for retry; an
existing processing marker is retried before claiming a later pending bit.
The parent directory and claimed-marker identities are rechecked before ack.
No pending work is deleted merely because another worker is busy.

This is boot-scoped state. Normal boot retains the existing initial hotplug
scan and after-Graywolf wiring pass. Markers are delivery bookkeeping, not
trusted free-radio evidence. Successful acknowledgement means the existing
apply invocation returned zero; that coordinator can defer wiring for an
inactive owner, so it does not prove completed hardware/program convergence.

## Manager delivery

The proposed path unit watches only `marks`; the persistent worker lock is
outside it, so an empty queue does not continuously trigger work. Its service
runs after the existing boot hotplug and Graywolf units and does not become a
session prerequisite. It uses `KillMode=control-group` and a bounded startup
timeout. No apply/generation lock is added across a caller's service wait.

Ordinary failures wait five seconds before returning. Service start limiting
bounds failures that occur before the helper can run. An independent timer
backstop retries at fixed half-minute calendar boundaries, on boot and clock
changes. It does not rely on a service activation/inactivity timestamp advancing
after a start-limited job. These are retry triggers, not an end-to-end latency
guarantee; apply lock waits and owner readiness still have their own bounds.

## Activation and rollback gate

Fresh provisioning keeps the existing direct udev producer and does not copy
or enable the reconcile units. `DXB_RADIO_EVENT_QUEUE=on` is an explicit
experimental test opt-in for the producer and unit activation; it is not a user-facing setting
or permission to operate a radio. An unset flag does not automatically retire
units that an operator previously enabled experimentally.

Before activation is supported, whole-tree update and rollback must safely
quiesce/retire the consumer when changing to a tree without this helper, retain
pending requests, handle provisioning/swap failure, and recover when a queue
unit is failed or start-limited. Ordering, cancellation and actual udev/path/
timer delivery need a real manager check. The queue remains inactive on the Pi
while those items are open.

The subsequent [update barriers](radio-event-update-gates.md) add a shared
update lease to reconciliation and experimental publication, and refuse a
whole-tree change when queue configuration, work/history or unknown manager
state remains. Protocol 2 reconciliation requires the matching `common.sh`
helper. These barriers do not implement automatic retirement or recovery.

The later [publication preflight](radio-event-publication-preflight.md) also
requires the shared-library functions and a bounded, successful protocol-2
query before opt-in unit/rule writes. It refuses incomplete deployments while
leaving the default-off path unchanged. This is not complete bundle authority.

This queue also does not supply handover pending intent, source/invocation
validation, device continuity or an installed protocol/free-state verifier.
Those remain in the [mutation-phase work](radio-mutation-phases.md).

## Verification evidence

Nine Docker groups execute the producer/worker with real files, processes and
kernel flock while the apply command is a fixture. They cover burst coalescing,
arrival during a blocked apply, failure with later work, a killed process group,
busy worker retention, producer independence from held record/apply/worker
locks, descriptor closure, unsafe queue objects, unit integration and default-off
activation. All nine focused groups passed. They do not constitute actual
systemd, radio, audio or RF acceptance.

Offline `systemd-analyze verify` accepted the final service/path/timer with a
public read-only helper mount, read-only root, no capabilities and no external
network. A separate unprivileged container correctly rejected enqueue before
creating state. These checks do not start a manager.

Manager setup attempts did not establish a usable manager: the D-Bus bootstrap
reported a forbidden FD-limit increase; setting the container limit was rejected
before process startup; a direct user-manager probe exited 1 without retained
diagnostic output. No privileged host/cgroup access or Pi test was substituted.
Evidence is retained at `/workspace/dxberry-radio-event-tests/`.

The subsequent [manager prerequisite investigation](../../tests/integration/README.radio-event-manager.md)
identified the manager binary path and a working private bus query. With console
logs enabled, manager allocation fails on the read-only cgroup2 hierarchy.
The descriptor-limit warning is separate from that final failure. Actual manager
acceptance remains open; the read-only preflight returns 77 in this environment.

The first isolated maintenance run exposed a copied development build list
requiring deliberately excluded native/capture files. The baseline maintenance
list was restored and extended only with queue packaging entries. Early test
runs also preceded the independent timer backstop and default-off gate; those
intermediate results are not final implementation evidence.

The full shared-shell run then exposed a no-op `sleep` function left by earlier
fixtures. Real concurrency polling and timeout/PS probes now use `command` to
bypass those stand-ins. All nine focused tests also passed with a deliberately
shadowed `sleep`, reproducing that harness condition. Clean full runs follow
those corrections.

The final Docker runs passed 1,023 development and 935 isolated maintenance
shell tests with zero failures, followed by Bash syntax, ShellCheck and tree
checks in both snapshots. All nine queue groups passed within those shared
full runs. The offline unit parser and unprivileged refusal checks also passed.
No production Go or UI code changed, so their broader suites were not repeated.
These results do not close manager, rollback or hardware acceptance.

## Inactive Pi candidate staging

Source `231da383404a14ae19e1794f00800f232efee04d` was staged through DockerPC at
`2026-11-23T16:29:01.484379+00:00`, under
`/root/dxb-radio-event-candidate-231da38`. This is a candidate directory, not an
installation. No activation/installation script is included. The seven-file
payload contains the helper, isolated udev/rigctld library changes, queue
service/path/timer templates and runtime-directory template. Existing file
hashes were pinned against the actual Pi before staging; development-only
features remain excluded from this isolated candidate.

All three transfer pieces and the 7,679-byte archive were verified (SHA-256
`4e7016998b2bd4ec92a2bb83295a29117baa2d5991d3bb1dc188c45629f6de6a`).
After extraction, file hashes, root ownership, modes and single-link regular
file types matched the candidate manifest. Existing active files still matched
their previous hashes; the new helper/templates and manager units were absent
from the active tree/configuration. The staging record remains in that private
directory as `staging-record.json`.

Separate post-staging read-only diagnostics verified the previously installed
handover-presence patch and its saved original, the `.4` console, all seven
unchanged service identities, checked radio/TX/helper/network files, Graywolf
configuration and mixer. Both PCM streams remained RUNNING. No service was
started/reloaded, no radio operation or registration was issued, and no active
runtime file was replaced. The candidate remains off pending the manager and
whole-tree update/rollback work above.

That protocol-1 archive remains retained. The subsequent
[update-barrier record](radio-event-update-gates.md#inactive-protocol-2-candidate)
documents the matching protocol-2 candidate and its shared-library dependency,
staged separately while the queue remains inactive.
The newer [publication-preflight candidate](radio-event-publication-preflight.md#inactive-pi-staging)
adds incomplete-library and helper-protocol refusal checks. It is also staged
privately with no installer; earlier candidates remain retained.
