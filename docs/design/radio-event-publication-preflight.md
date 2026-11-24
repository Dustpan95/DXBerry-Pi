# Protocol preflight before radio queue publication

Status: experimental candidate only. The queue stays off by default and is not
running on the Pi. This check closes an incomplete-deployment refusal gap; it
does not establish complete installed-bundle authority or production acceptance.

## Publication boundary

Explicit opt-in previously allowed unit/rule publication without checking that
the installed helper supported the queue/update protocol. A missing or older
helper could therefore leave configured producers and consumers that could not
satisfy the reconciliation/update contract. The [update refusal barriers](radio-event-update-gates.md)
would then correctly prevent a release-tree swap, but could not repair that
partial deployment.

Both experimental publication entry points now require the shared update lease
and protocol-preflight functions to exist. Inside that lease, and before their
first configuration write or manager action, they query the installed helper.
Missing library support or an unsafe, unavailable, incompatible, failed, hung
or changed helper returns 6. Existing rules/fragments remain untouched by the
refused publication. This does not undo earlier full-provisioning steps or
retire configuration/consumers that were already present.

Default-off publication neither requires nor executes the protocol query and
keeps the existing direct producer and boot units. Pure rule generation remains
side-effect free; the check belongs to the write operation.

## Bounded query and continuity checks

Normal publication queries the fixed installed
`/opt/dxberry/bin/dxberry-radio-event protocol`. A trusted root/test environment
can supply `DXB_RADIO_EVENT_PROTOCOL_CMD`; request JSON cannot select it.
The helper must be an executable, root-owned, single-link regular file with
mode 0755 and size 1–65,536 bytes. Its immediate parent must be a non-symlink
root-owned directory with mode 0700, 0750 or 0755. The helper itself cannot be
a symlink.

The query must exit zero and emit exactly `2` plus one newline. Stdout is kept
in a private boot-scoped temporary directory with a one-KiB file-size limit;
stderr is discarded. The real `timeout` command applies a three-second execution
limit and a one-second TERM grace before KILL. These bounds concern the query,
not every filesystem read or the complete publication operation. Ordinary exit
removes the private probe directory without changing the caller's traps.

Before allowing publication, the check revalidates the observed parent identity,
helper inode/ownership/link count/mode/size and SHA-256. A replacement or byte
change during the query refuses publication even when the reply was `2`.
The existing shared update lease remains held through the probe wait and later
publication, so a cooperating public updater cannot cross either phase.
The protocol query itself initializes no queue and invokes no radio operation.

These are admission and observed-continuity checks. They do not prove immutable
execution, reject all ABA changes by external root writers, bind every loaded
function/unit/module/profile, or verify a running worker's invocation and
descendants. The parent check is not descriptor-bound traversal of all
ancestors. A protocol report is not free-radio evidence or permission to operate
hardware. Complete deployment binding, manager delivery, retirement and recovery
remain open before activation.

## Verification

The positive case executes a copied production helper's actual read-only
protocol query. Configuration writes and manager actions remain fixtures.
Nine new Docker groups cover incomplete shared libraries, missing/old/failed/
ambiguous replies, unsafe helper/parent metadata without execution, bounded
oversized output, a hung query, byte/path changes, positive publication, the
retained update lease during a wait and unchanged default-off behavior.

All nine new groups passed on the final source. Against the previous
implementation, seven groups failed while the two compatibility/default-off
groups passed. All 28 related queue/publication/update-barrier groups passed
in focused development and maintenance runs. Early lint findings in a boolean
chain and literal fixture text were rewritten before final verification.

Review then corrected the runtime-directory default and made resource-limit
setup failure refuse before helper execution. The public default-directory
case passed. Early full runs were interrupted for that correction and are not
acceptance evidence. Fresh final Docker runs passed 1,042 development and
954 isolated maintenance shell tests with zero failures, followed by complete
Bash syntax, ShellCheck and tree checks in both snapshots. Those full runs
include all 28 related groups and the final default-directory case.

The isolated package scope matched the exact live baseline while preserving
development feature exclusions. Tree checks used nondeployable executable
zero-byte binary placeholders in disposable snapshots; the explicit candidate
payload excludes those binaries. No production Go or UI code changed, so their
broader suites were not repeated. Evidence is retained in
`/workspace/dxberry-radio-bundle-tests/`.

No queue activation, actual manager delivery or Pi execution test follows from
these checks. The current Pi update safeguard and manual radio/TX/audio setup
remain separate from this experimental candidate.

## Inactive Pi staging

Source `d36d77f40d78bb909f4285f8e2320453784b365e` was staged through DockerPC
at `2026-11-23T17:42:05.836710+00:00`, under
`/root/dxb-radio-event-candidate-d36d77f`. The eight-file protocol-2 payload
includes the matching common-library functions and isolated publisher changes.
It has no installer. Earlier candidates remain retained separately.

All four transfer pieces and the 11,844-byte archive were verified (SHA-256
`90906c36d112c26f0a7f5fa31a297ac9de66e92039b0bd790eb049380e812211`).
Extracted file hashes, root ownership, modes and single-link regular types
matched the manifest. Active counterpart files retained their pinned hashes;
the new helper/templates were absent from the active tree and all three manager
units remained positively absent. The private directory retains its
`staging-record.json`. No placeholder binary was deployed.

Separate read-only diagnostics verified the existing update safeguard from
source `2d1ade8`, its saved original and unchanged `.4` console. All seven
service identities, checked radio/TX/helper/network files, Graywolf configuration
and mixer were unchanged; capture and playback remained RUNNING. No active
runtime file was replaced, no service was started/reloaded and no queue helper,
registration or radio operation was executed on the Pi. The new preflight and
queue remain inactive pending the acceptance work above.
