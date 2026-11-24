# Update job execution lock

Status: implemented and installed as an isolated Pi maintenance patch.
The console remains `.4`; production preset authority remains unavailable and
the development generation helper remains uninstalled.

`update.lock` serializes job admission. The transient job runs in another
process and must not rely on that admission descriptor remaining held. The
public `dxberry-update apply KIND [NAME]` path now takes a separate exclusive
`$DXB_RUN_DIR/update-apply.lock` before entering its body, invalidating cache or
publishing a result. This covers Graywolf, tree update, rollback, system update,
app update, app install and app removal. Refusal returns 6 and preserves the
existing cache and result instead of overwriting another job's evidence.

## Lifetime and lock order

`cmd_apply` runs in a subshell with its own dynamic descriptor. The gate remains
held through body execution, session recovery, cache invalidation, result
publication and EXIT cleanup. Every normal return or exit closes the command's
copy. Descendants can retain an inherited copy longer; the guard does not
explicitly unlock work that may still be running.

The default wait is 30 seconds, selected for trusted configuration by
`DXB_UPDATE_APPLY_LOCK_WAIT`. The final runtime directory must be root-owned,
real and mode 0700, 0750 or 0755. The fixed lock must be root-owned, regular,
single-link, empty and mode 0600. Missing files are created exclusively; invalid
existing objects are refused without repair or truncation. The descriptor is
opened read-only and bound back to the unchanged directory/path after waiting.

The execution gate is distinct from `update.lock`, `config.lock`, the radio
record/apply locks, the generation lock and `provision.lock`. An update that
calls provisioning holds update execution first, then the provisioning child
takes its own gate. Unit/native hooks and emergency STOP take neither job gate.
Native release reconciliation keeps its separate coordination and fresh-job
checks; an available admission lock alone does not authorize resume.

Job/status reads remain available while the execution gate is held. Existing
body result/exit behavior remains unchanged. A direct low-level library call
does not acquire this command boundary and needs its own verified caller scope.

## Verification and remaining work

Docker regressions run the real CLI main with simulated job bodies and actual
kernel flock contention. All seven kinds refuse a held shared gate before
calling their body, removing cache or replacing a previous result; a later
retry proceeds after release. Other fixtures keep readers out during a delayed
body, delayed result publication and EXIT cleanup, and verify closure afterward.
The body can still obtain a shared legacy admission lock and job status can
still be read. Fault cases cover missing/failed flock, unsafe types, links,
permissions, content and replaced directory/lock evidence. The prior command
fails all five added groups. These are command/process/filesystem fixtures,
not real package installation, systemd workload, RF or audio acceptance.

This gate does not establish a free radio or complete job/process tracking.
Remaining work includes settings-job phases, record/apply/caller boundaries,
reader-side protected ancestor traversal, installed source/protocol/profile
binding and fresh unit/job/process/restoration evidence. The legacy global
result document must not be treated as authoritative lifecycle evidence.
The [request/result follow-up](update-result-binding.md) now binds presentation
to the admitted request and checks that the execution gate is free. A gate
alone cannot prove that prior job descendants have finished. Preset target
authority remains unavailable.

Evidence is retained at `/workspace/dxberry-update-execution-tests/`. No Go or
UI source changes are part of this implementation.

All 971 development shell tests, repository Bash syntax/ShellCheck and the tree
check passed in Docker. The focused runner counts both failed assertions and
early nonzero test exits. Snapshots use non-deployable compiled-helper
placeholders; Go, cross-build and browser checks were not repeated for this
shell-only change.

## Pi maintenance installation

The isolated patch was applied on 2026-11-23 at 12:14:43 UTC. Only
`/opt/dxberry/bin/dxberry-update` was replaced, preserving root ownership and
mode 0755. Removing only the new helper, descriptor declaration and command
subshell boundary reproduces the exact prior installed command; no other
development changes or generation-helper dependency were added.

| Item | Record |
| --- | --- |
| Source commit | `5c636febdeeeaa865fe6358fc67a54f54e34127e` |
| Original SHA-256 | `defbb72eee3a48ecf12985faf9cd2b1728f6fa62ee89ec01b67e0f4bcafb567f` |
| Installed SHA-256 | `1a96f5994acc9fd2312baa31ee8d85e090c0930dda4eb1170aeae79c5f51a2b5` |
| Installation record | `/var/lib/dxberry/local-patches/update-execution-lock.json` |
| Retained original/checks | `/root/dxb-update-execution-5c636fe` |
| Verified staging | `/root/dxb-update-execution-stage` |

All 882 isolated maintenance shell tests, focused execution-gate checks,
Bash syntax, ShellCheck, tree checks and actual atomic-copy/rollback fixtures
passed in Docker. The archive was 7,123 bytes, with SHA-256
`e17b90b6ad4577137a0222508ce4c4aafbbfa2419f60972d9bb0e4256666bf62`.
Both upload pieces, the complete archive and staged script bytes/modes were
verified before replacement.

Installation acquired update execution before the separate provisioning gate,
then the existing update/settings/record/apply locks. It refused ordinary active
apply/provisioner processes and pending jobs, pinned the `.4` console and old
script hashes, retained and synchronized the original, and used atomic
replacement with synchronized directories. The new empty runtime gate remains
root-owned, mode 0600. It was acquired only for installation; no update body or
provisioning command was run.

Installation and a separate read-only follow-up verified the installed and
backup hashes, unchanged identities of all five services, previous maintenance
files/records, radio/TX/helper/network files, mixer and Graywolf configuration.
Both PCM streams remained RUNNING; HTTP and verified HTTPS version checks
passed. No services were restarted and no radio commands issued. The generation
helper and epoch directory remain absent. This is reversible file maintenance,
not package/systemd workload or physical acceptance. Evidence remains at
`/workspace/dxberry-update-execution-tests/`.
