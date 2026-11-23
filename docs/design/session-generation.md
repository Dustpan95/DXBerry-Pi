# Session and native acquisition generation guards

Status: development source only. The Pi keeps its verified `.4` console and
maintenance patches. Production preset authority remains unavailable.

Session and native operations can change routing or future ownership without
saving a radio record. Their positive acquisition/maintenance entries now
invalidate earlier generation evidence before live work.

## Shell session boundaries

`dxb_app_session_start` advances before clearing its waiting marker, enabling,
resetting or starting the session unit. Refusal returns 6 to its caller and
retains the marker; existing CLI result mapping remains unchanged.

`dxb_screen_prepare` first checks the session, owner, presence and wiring
metadata, then advances before settings/first-fill/logbook work, runtime files,
companions or audio preparation. Its unprivileged settings child does not gain
root-helper access. The root parent owns this boundary. Later preparation or
service failure retains the new generation.

The unit's existing first activation condition still advances independently.
There is no skip flag for a request already stamped by another layer.

## Native boundaries

| Entry | Before live work |
| --- | --- |
| Explicit native initialization | Prepare the trusted station parent and advance before inventory creation |
| Native approval Enable | Advance before the service mutex and approval work |
| Configured native start client/server | Advance before IPC or taking the service mutex |
| Native release pause/preflight | Client and server explicitly prepare/advance before IPC or pause/audit |
| Native release resume | Advance before IPC or taking the service mutex |

The service's unconfigured start branch can only return a default-off no-op or
refusal; it does not enter a controller initialized concurrently. A missing
inventory with a cached controller is refused. Compatible-bundle checks and
read-only status/preview keep their existing nonmutating behavior.

Native initialization and release-pause use the existing trusted-parent API.
Only the final station directory may be new; unsafe existing paths and corrupt
epochs remain refused. Ordinary activation/start advances do not create a parent.
The explicit provisioning helper opcode and browser denial are unchanged.

## Cleanup and lock ordering

Emergency PTT STOP, session stop, stopped hooks, native Disable and Recover gain
no tracking requirement. Cleanup can restore owned state or reduce consent;
fresh authority must still reject pending journals, unfinished restoration and
active/deactivating unit jobs. An inactive unit alone cannot establish free state.
Normal program-stop retains its existing record-lock scope.

Native acquisition advances before entering the service mutex. A failed or
blocked tracker must not hold that mutex against stopped/recovery operations.
This preserves the cleanup path without claiming hard real-time filesystem or
service behavior. No failed operation restores an earlier generation.

## Evidence and remaining work

Docker shell fixtures cover prepare/start with missing, uncertain or unexpected
tracking responses, retained waiting markers, later preparation failure, and
stop/unkey/audio cleanup with unavailable tracking. Device/service/settings
actions are markers; no physical radio is registered or controlled.

Go fixtures use real generation files, private application-user routing data
and Unix sockets with simulated lifecycle gates. They check broken tracking
before approval/start/release work, refusal before native IPC, initialization
before inventory writes and restoration through a broken tracker. A kernel-lock
fixture observes acquisition's second open description for its private lock
inode, then completes the stopped hook while that writer remains blocked.
These are not real WSJT-X package or systemd workload acceptance.

Whole-job exclusion, effective installed unit/helper/module compatibility and
fresh free-state authority remain required. Direct low-level component/settings
or routing primitives outside the guarded entries still need coverage or a
verified invocation boundary. A generation is not a lease or CAT permission.
The preset `TargetSource` remains unavailable; no preview/recall/undo is enabled.
Evidence is retained at `/workspace/dxberry-session-generation-tests/`.

The later [native mutation leases](native-mutation-leases.md) now retain the
exclusive generation lock through the named local native mutation boundaries.
They split client/server ownership, move resume authority acquisition outside
the service/generation locks and recheck pause/receipt evidence after waits.
Shell mutation phases and the full production verifier remain open.

All 962 shell tests, repository Bash syntax/ShellCheck and the tree check passed
in Docker. Full Go vet/race passed; the added IPC/initialization checks and a
focused race run for the final kernel-lock concurrency fixture passed too.
Native and ARM64 builds produced all four executables as non-deployable local
fixtures. The six actual metadata-helper containers passed using the newly
built native helper. ARM execution, hosted CI and real manager/package/RF/audio
acceptance remain open. No Pi file or service was changed.
