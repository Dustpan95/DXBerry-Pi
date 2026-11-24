# Settings job execution lock

Status: development implementation with an isolated Pi maintenance installation.
Preset authority remains unavailable.

The settings lock serializes set/confirm/revert and the job's brief network-timer
recheck. It is not held through the whole worker, which can run provisioning
before returning to that settings lock. The apply command now takes a separate
exclusive `$DXB_RUN_DIR/config-apply.lock` before reading/consuming pending keys,
running provisioning or the Graywolf follow-up, or publishing its result.
Refusal returns 6 and preserves pending keys and the previous result.

## Lifetime and recovery

Only `cmd_apply` acquires this execution gate. Its subshell retains a dynamic
descriptor through provisioning, optional Graywolf position/GPS follow-up,
network-timer recheck, pending-key removal, result publication and EXIT cleanup.
The command's copy closes on every return/exit. Descendants can retain their
copy longer; the guard never explicitly unlocks unfinished child work.

The default wait is 30 seconds, with trusted configuration through
`DXB_CONFIG_APPLY_LOCK_WAIT`. The final runtime directory must be real,
root-owned and mode 0700, 0750 or 0755. Its fixed lock is root-owned, regular,
single-link, empty and mode 0600. Creation is exclusive; existing unsafe objects
are refused without repair/truncation. The descriptor opens read-only and is
checked against the unchanged directory/path after waiting.

The execution gate is distinct from the short `config.lock` and from update
execution/provisioning gates. A settings worker retains its execution gate
while a provisioning child takes `provision.lock`. Confirm and revert acquire
only their existing settings lock, so they do not wait for this execution gate.
After provisioning, apply still checks pending network state after acquiring
that short lock; a cleared snapshot does not re-arm the timer.

This preserves recovery's existing locking scope. It does not establish that a
real provisioning process cannot continue writing after an independent network
restore, or supply new job-cancellation/manager acceptance. Console network
configuration remains disabled by default, and this change does not enable it.
The Pi's working network/radio configuration must remain unchanged during
maintenance validation.

## Verification and remaining work

Docker fixtures run the production CLI with simulated provisioning, Graywolf
and manager callbacks and temporary network files. Actual flock checks cover
contention before work, retained pending/result bytes, retry after release,
the provision/follow-up/result intervals, EXIT cleanup and descriptor closure.
Status remains readable. Confirm/revert run while a delayed apply owns its gate;
the worker then skips re-arming their cleared snapshot. Four added groups fail
on the previous command; the recovery-availability group also passes there.

Fault fixtures cover missing/failed flock, unsafe directory/file types,
permissions, links, content and replaced lock/path evidence. These establish
software command/filesystem behavior, not real network rollback, package,
systemd workload, RF or audio acceptance. Evidence is retained at
`/workspace/dxberry-settings-execution-tests/`.

The separate [settings result binding](settings-result-binding.md) identifies
the admitted request for completion presentation. The execution gate does
not cover arbitrary direct component calls or all record/apply caller scopes,
and does not prove that old descendants have finished. A preset verifier still
needs protected reader paths, complete lock ordering, installed source/profile
binding and fresh unit/job/process/restoration evidence. No preset target
authority is enabled by this change.

The full Docker development suite passed 983 tests with zero failures; the
isolated installed-baseline maintenance suite passed 894 with zero failures.
Both tree checks passed, as did Bash syntax and repository ShellCheck. Installer
fixtures verified atomic replacement, original-byte rollback and retained
diagnostics on an incomplete rollback. Go tests were not run for this shell-only
change. The isolated maintenance command matches development exactly; removing
only this gate restores the saved installed command byte for byte.

## Pi maintenance installation

Installed through the direct DockerPC connection at `2026-11-23T12:58:24Z`
from source `8e462851b0d48ddbbbd1f7b7ffe9e21be73897f5`. The one-file patch
replaces only `/opt/dxberry/bin/dxberry-config`, root-owned mode 0755:

| Item | SHA-256 |
| --- | --- |
| Installed command | `349a0c308aadaed1297740ce600db7f0bfb6d7fc3ee8b5207851f6c21f0e7615` |
| Saved original command | `06c113177d0c3b1af64d2be82d1930ec85e0a66422a45f7f58e185c897f45e30` |
| Uploaded maintenance archive | `db741ffd6792958d05213ed51a15e24163b601d57ea5098cae4f054bac06496e` |

The verified original is retained at
`/root/dxb-settings-execution-8e46285/bin/dxberry-config`; the installation
record is `/var/lib/dxberry/local-patches/settings-execution-lock.json`.
The later [settings result-binding installation](settings-result-binding.md#pi-maintenance-installation)
supersedes this installed command hash and retains the same execution guard.
Replacement used fsync and atomic rename, with original-byte rollback on failure.
The installer acquired update, settings and provisioning execution gates before
the existing admission, record and apply locks, and refused active jobs or
provisioners. It did not run settings, update or provisioning work.

Independent read-only diagnostics verified installed/backup hashes, the
unchanged `.4` console, all five service identities, radio/TX/helper/network
files, Graywolf configuration tables and mixer settings. Both PCM streams
remained RUNNING. No services were restarted, no radio commands were issued,
and no generation helper or epoch directory was installed. Settings status
reported no pending change or job. These checks establish maintenance integrity;
actual settings/network recovery and manager workload acceptance remain open.
