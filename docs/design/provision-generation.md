# Provisioning generation guard and trusted bootstrap

Status: development source only. The Pi retains its verified `.4` console and
maintenance patches. No tracking feature or new helper is installed there.

A valid full provisioning run now requires tracking before its configured
system, account, network, package, radio and program steps. Its existing state
root creation/protection precedes the guard. Refused tracking returns 6 before
those steps, completion/status publication or first-boot reboot. The caller
does not fall back to an untracked run.

Read-only `--check`, help and already-completed first boot do not advance.
Existing missing/invalid configuration diagnostics and their first-boot fallback
remain unchanged; these diagnostic/power paths are outside the valid-run guard.
This is not a transaction over every metadata byte or every power operation.

## Explicit root metadata operation

The helper now supports `prepare-advance` alongside ordinary `advance`, with
the same optional explicit `--state` argument, root check, private JSON protocol
and process deadline. Only the provisioner requests that helper opcode; explicit
native initialization/maintenance may use the same Go preparation API. Browser
CLI access still rejects the helper entirely.

`Store.PrepareParent(ctx)` opens the existing immediate ancestor through the
same root-owned, protected, non-symlink directory walk. It creates only the final
configured directory with mode 0700. Missing ancestors, symlinks, non-directories,
foreign ownership and writable paths refuse the operation. Existing protected
directories are validated without chmod/chown. The helper does not recursively
create ancestors or repair an existing directory, private tracker or epoch.

Preparation syncs the directory and its ancestor, then checks fresh path
identities. A failure can leave an empty new parent. Preparation alone does not
initialize or rotate an epoch. `prepare-advance` follows successful preparation
with the existing exclusive-lock advance; corrupt private state still refuses.
Neither a later failure nor uncertain delivery restores an old generation.

Normal `advance` remains strict about a missing parent. Activation, package and
radio clients keep using it. Boot/radio tmpfiles still declares the parent
policy; provisioning no longer depends on reaching that later setup step to
create its first tracking parent. Custom trusted paths are passed as argv, not
interpolated into a tmpfiles configuration.

The combined operation uses the existing three-second command context and
3.25-second process watchdog. Filesystem/entropy checkpoints are not hard
real-time syscall cancellation. Callers retain their own deadlines. A failure
is not proof that no bootstrap metadata was created.

## Evidence and remaining work

Go storage fixtures check explicit creation, unchanged epoch/unrelated bytes on
prepare-only reruns, corrupt epoch refusal, unsafe leaves/ancestors, cancellation,
sync failure and path replacement. The non-Linux implementation is unavailable.
CLI fixtures check operation selection, real-UID refusal and browser denial.

Shell fixtures exercise ordinary, reseed and first-boot refusal before a first
live-step marker, read-only/completed no-ops and repeated tracking before a
deliberately failed first step. Their helper responses are metadata fakes.

The actual-helper driver adds a sixth fresh root container with no precreated
station parent. It checks bootstrap and repeated durable advances, missing
ancestor refusal, unsafe-mode/symlink/file rejection without repair, corrupt
epoch refusal and retained unrelated data. The old helper fails this new mode;
the rebuilt native helper passes. All containers use private tmpfs, read-only
roots, no capabilities/network and no radio devices. No systemd manager or
configured provisioning step runs in this fixture.

The [session/native guards](session-generation.md) also cover positive
acquisition and release-maintenance entries. This provisioner entry guard does
not cover every direct call to component/native/routing primitives
or exclude writers through a whole asynchronous operation. Effective installed
unit/module/helper compatibility, fresh program-free evidence and exclusion
through CAT write remain required. STOP and stopped-hook cleanup are unchanged.
Production preset authority remains unavailable. Real systemd/workload,
first-boot, RF/audio and package acceptance remain separate gates.

Evidence is retained at `/workspace/dxberry-provision-generation-tests/`.

All 959 shell tests, repository Bash syntax/ShellCheck and the tree check passed
in Docker, along with full Go vet/race and tagged-fixture vet. Native and ARM64
helpers built; the ARM64 fixture and non-Linux helper tests compiled only.
All six actual-helper containers passed. The earlier helper rejects the new
operation, confirming that callers cannot rely on an old binary for bootstrap.
No ARM execution, hosted CI, real manager, configured first-boot or RF/audio
acceptance is claimed.

The later [full provisioning operation lock](provision-operation-lock.md) now
serializes valid full runs before state creation and tracking. It remains
separate from unit/native hooks and adds a completion recheck after waiting.
Other shell phases and the full preset verifier remain open.
