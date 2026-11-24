# Package mutation generation guards

Status: development source only. The Pi retains its verified `.4` console and
separate maintenance patches. Production preset authority remains unavailable.

Package changes can replace a program, stop a session, change account/device
access or pause native acquisition without saving a radio record. These job
entry points now require a successful generation advance before live work.

## Shared guard and boundaries

`dxb_program_generation_advance` lives in `common.sh`, so radio and package
clients share the same protocol. The fixed default helper, trusted operator/test
override, state-path argument, exact canonical success response and exit-6
refusal are unchanged. The diagnostic now covers program and radio ownership.
Raw helper output is never printed. Reads/status do not invoke the guard.

| Operation | Before advance | Work after advance |
| --- | --- | --- |
| Program install/direct update | Validate mode/catalog/source; refuse the built-in program | Accounts, download, package work, module hooks |
| Program removal | Re-read ownership under the record lock; refuse an in-use program | Stop/disable, package removal, unpacked-file deletion, module cleanup |
| Program update job | Refuse unknown, built-in or uninstalled programs | Session enumeration/stops, installer, recovery attempt |
| Graywolf update | Read configuration; return normally if pinned | Package installer and service handling |
| System update | Accept the requested operation | `dpkg --configure -a`, package-list update, upgrade/scripts |
| DXBerry tree update | Fetch/check/extract candidate; validate basic release metadata | Native release preflight, live tree transitions, setup |
| Tree rollback | Require a usable previous tree | Native release preflight, tree transitions, setup |

Native release preflight can pause acquisition without a subsequent swap, so
both tree operations advance before invoking it. Tracking refusal during an
update removes the staged candidate and leaves the live tree in place.
Downloads/staging and eligibility checks are not a transaction over every byte.

No-op or invalid requests returning before their boundary do not advance.
An eligible attempt advances even if a later step fails or final program bytes
are unchanged. System update advances before its first `dpkg` command, because
package configuration itself can have live effects.

## Failures and recovery

The update job advances before stopping sessions; its nested installer advances
again. There is no global skip flag. If the second advance fails, the existing
session recovery is still attempted. A session's own activation condition remains
mandatory, so attempted recovery does not prove it resumed with broken tracking.

No failure restores the old generation. Tree restoration, failed preflight,
failed installation or a later restart failure can leave an advanced token and
partial earlier work. Exit 6 is not a universal promise that no account, session,
tracking or staging state changed. Existing package/tree result codes and
recovery policies remain. Emergency PTT STOP and stopped-hook cleanup gain no
tracking dependency.

## Evidence and remaining work

Docker fixtures cover seven entry points with a missing helper, uncertain
success delivery and an unexpected response. Account/package/service actions
and preflight are markers; tree jobs use synthetic release trees. Refusal must
not reach a live marker, swap the active fixture tree or run its provisioner.
Additional fixtures cover eligibility/no-op cases, repeated later failures
retaining advances, and nested installer failure still attempting recovery.
These fixtures use metadata-only generation responses.

The actual compiled-helper suite separately reruns all five isolated
root/unprivileged/activation/record/apply fixtures through the common guard.
It checks durable generation and runtime-mirror behavior, with read-only roots,
no capabilities/network/radio devices and private tmpfs. This is not real
package or service compatibility acceptance.

The guards invalidate earlier evidence; they are not whole-job locks and cannot
authorize a preset. [Valid full provisioning](provision-generation.md) now has
its own entry guard and explicit parent bootstrap. Direct routing/native-control helpers,
effective unit/module/helper binding, fresh program-free evidence and exclusion
through CAT write remain open. Replaced/older releases also require fresh
compatibility verification. Real packages, systemd workload, first-boot and
RF/audio acceptance are separate gates. Evidence is retained at
`/workspace/dxberry-package-generation-tests/`.

All 956 shell tests, repository Bash syntax/ShellCheck and the tree check passed
in Docker, as did the five actual-helper fixtures. Go/helper code is unchanged;
the verified native helper was reused. No package, service or radio operation
was performed on the Pi during this source step.
