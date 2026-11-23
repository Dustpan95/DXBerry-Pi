# System upgrade preparation

Status: implemented and installed as isolated Pi maintenance. The change is
limited to the existing system-upgrade preparation path.

The availability checker already requires a successful refresh from every
repository. The upgrade worker previously used APT's default refresh behavior,
which can return success while retaining old indexes after a repository fails.
It also parsed the simulation through a pipeline without explicitly checking
APT's exit status. A failed simulation with partial package output could still
lead to an upgrade and misleading restart bookkeeping.

The worker now refreshes with `APT::Update::Error-Mode=any` and captures a
successful simulation before parsing its package list. Either failure returns
preparation code 6 before the real upgrade, program restarts or reboot-flag
publication. This behavior does not depend on the caller enabling `pipefail`.
Actual upgrade failure retains code 8. Existing configuration retention, package
lock waiting and program/reboot handling after a successful upgrade remain.

The existing interrupted-configuration repair still runs before refresh. That
repair can itself change package state; preparation failure does not mean that
all earlier effects were rolled back. Existing generation admission in the
development source is also retained. The isolated installed library has no
development generation dependency.

## Verification

Two Docker regressions reproduce partial repository failure and a failed
simulation that emits a kernel-package prefix. Both fail against the previous
function. The new function refuses simulation/upgrade after failed refresh,
and refuses upgrade, program restart and reboot publication after failed
simulation, including with the caller's `pipefail` disabled.

A real-APT Docker fixture uses an unreachable private-loopback repository,
isolated APT paths, no external network, a read-only root and no capabilities.
Default refresh exits 0; strict refresh exits 100. The previous worker continues
and returns success in that empty disposable package environment. The corrected
worker returns 6 before simulation or upgrade. Interrupted-dpkg preparation and
generation admission are explicit fixtures in this check; no Pi package work,
radio command or real systemd acceptance is represented by it.

The existing upgrade-failure fixture now fails only the real `-y` invocation,
allowing simulation to succeed before verifying the existing code-8 behavior.
Evidence is retained at `/workspace/dxberry-update-worker-tests/`.

The full Docker runs passed 1,002 development and 914 isolated maintenance
shell tests with zero failures, followed by Bash syntax, ShellCheck and tree
checks in both snapshots. The maintenance suite also includes the existing
command-link fixture from the development update tests; this is not evidence
that the tracking helper was installed. No production Go or UI source changed.
Atomic replacement and complete/incomplete rollback fixtures passed. The first
full-suite launch used read-only mounts that prevented build fixtures from
moving their placeholder binaries; writable disposable copies corrected the
harness before both full successful runs. An obsolete second-library reference
in the single-file rollback fixture was also corrected before its final pass.

## Pi maintenance installation

Installed through DockerPC at `2026-11-23T15:10:30Z`, from source
`82368817f54426a9f688c2b23674c4e6be51a2d7`. The only replacement is
`/opt/dxberry/lib/update.sh`, root-owned mode 0644:

| Previous SHA-256 | Installed SHA-256 |
| --- | --- |
| `9dce0fc05c827a0d649956add2ddc44d46b11277c88d677ca19a7fe8c72aa790` | `62e9be225cd94eb2a5c58d047ca8043ebbd5137c8b417d68e392403d0ba6e1ee` |

The exact previous library was retained at `/root/dxb-update-worker-8236881`;
staging remains at `/root/dxb-update-worker-stage`. The installation record is
`/var/lib/dxberry/local-patches/update-worker-preparation.json`. This supersedes
the update-library hash in the earlier freshness record; the status library and
all other local patches remain unchanged. The isolated candidate replaces only
the preparation function, without installing a generation helper or epoch.

All six transfer pieces and the 16,923-byte archive were verified (SHA-256
`3e4b920522c4644f587568265a2830f96a6f4751616073ebdca4f6eaad8c7fff`).
The installer checked the exact old library/console hashes, required no pending
or active jobs, retained the existing operation gates, saved and fsynced the
original, and used atomic replacement with rollback on post-check failure.

Separate read-only diagnostics verified the installed and original hashes,
ownership/mode, all seven unchanged service identities, the `.4` console,
checked radio/TX/helper/network files, Graywolf configuration and mixer. Both
PCM streams remained RUNNING; installer HTTP and verified HTTPS version checks
passed. No update/settings/provisioning job, package upgrade, service restart,
radio registration or transmission was started by this patch installation.
Hardware and actual package-workload acceptance remain separate.
