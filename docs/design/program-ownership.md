# Program ownership evidence hardening

The current console is `.4`, including the uncertainty follow-up below. The
earlier `.2` installation record is retained; the subsequent
[audio lifecycle maintenance](browser-audio-lifecycle.md) and `.4` update retain
those ownership changes and the installed screen library.

The cached program-holder guard must distinguish a verified stopped program
from an unavailable or ambiguous answer. Two failures were reproduced in Docker:

- A successful command returning missing/unknown state, duplicate fields or
  another session's answer could release a previously running program's hold.
- After switching a radio from WSJT-X to Pat, a failed Pat probe reused WSJT-X's
  earlier held/free answer and label. An earlier free answer allowed a browser
  claim or takeover before the new owner had been verified.

## Change

The screen probe now requires the exact requested session, explicit access,
and a recognized state. Canonical key spelling, duplicate detection, field
types, UTF-8 and a 64 KiB response bound protect that evidence. Optional app and
radio identities must agree with the requested session. Only `stopped`/`failed`
are valid free answers; `running`/`starting` hold the radio. Missing, unknown,
mismatched or ambiguous evidence is an error and cannot replace a held answer
with free state. Cancelled reads cannot supply an answer.

The CLI's screen-state library now obtains `ActiveState` through one successful
`systemctl show` read. Manager failure, empty output and unfamiliar state report
`unknown`, rather than inferring stopped from two failed commands. Active,
reloading, refreshing and deactivating units report running; the program keeps
its hold through stop/cleanup. Activating reports starting, inactive stopped,
and failed failed.

Cached probe evidence now belongs to the radio's current owner. Changing that
owner discards the old answer and label; an unanswered replacement holds the
radio until a valid probe answers. That initial `.2`/`.3` change retained an
unchanged owner's last-known answer policy; the follow-up below closes its
remaining free-state gaps. The polling cadence is unchanged. This cache is not fresh process or
systemd invocation evidence and must not be used as the identity authority for
the new Pat status client. That client still needs its separately reviewed
version/session/process checks before integration.

No transmit request, tuning, VOX/PTT setting, audio-device registration,
program start, buffer policy or modem implementation is added by this change.

## Follow-up maintenance `.4`

Four additional cases were reproduced in Docker: a failed probe after a known
stopped answer left browser claims/takeover available; an older slow refresh
could overwrite a newer held result; a canceled probe could publish free state;
and an assigned radio was claimable before its first probe completed.

Assigned radios now begin conservatively held until a successful probe answers.
An unavailable/canceled probe blocks browser ownership even after an earlier
free answer. Existing held labels are retained where appropriate. A private
nonzero-sized refresh token permits only the latest-started refresh to publish,
so a superseded free result cannot replace newer held/unknown evidence. This
token orders cache publication only: it is not a systemd/program lifecycle
identity and must not become the preset target's authority.

The subsequent [shell lock guards](radio-locks.md) also remove permissive
mutation fallback in development and a separate installed four-script patch.
They preserve the distinct emergency STOP path and do not turn this cache into
fresh program lifecycle evidence.

A verified stopped answer recovers ordinary browser use. A simulated-radio
regression verifies that uncertainty refuses a new carrier without sending a
second key-on, while STOP still unkeys and recovery restores the existing tab.
The assigned-owner binding and existing STOP implementation are retained.

The development tree and isolated maintenance source passed Docker Go vet and
all-package race checks. Twenty-five focused ownership runs and the STOP
regression passed. Ten Chromium generated-audio/PTT checks passed against the
isolated maintenance fixture, including STOP, disconnects, touch interaction
and the 30-second timeout. These checks are not physical audio/RF acceptance.
The `.4` maintenance copy differs from the verified
`.3` source in one production file, `internal/session/owner.go`, and three test
files. The unfinished preset, device-verifier, native WSJT-X and Pat-status
features are excluded. ARM64 built successfully; the candidate binary SHA-256
is `4d35861f720c58b4a86147f9c0265d3c4c2a1976946edbbfe107fdbbedb5cf14`.

The installer requires the exact `.3` binary/helper hashes, empty radio inventory
and successful update/configuration/record locks. It retains the old binary and
maintenance manifest, changes only the console binary and restarts only that
service. Before/after settings, mixer and independent-service identities must
agree. A failure attempts rollback without restoring or overwriting unrelated
settings. Staticcheck, actual program/service lifecycle and physical acceptance
remain open. Evidence is retained at `/workspace/dxberry-program-uncertainty-tests/`.

Installed through DockerPC at `2026-11-23T06:34:33Z` from
`ec8241056f2f1a129fff647f1df9ce0a4abbbf79`. The 194 transfer pieces, complete
compressed archive and unpacked binary passed integrity checks before execution.
HTTP and certificate-verified HTTPS returned `.4`, and the installed binary
hash matched. All five checked console/independent services were active;
Graywolf, nginx, netwatch and logbridge retained their process identities.
Graywolf configuration, radio/TX settings, mixer, helper and network files
matched before/after, and both CODEC PCM streams remained RUNNING. No radio was
registered, no modem or independent service restarted, and no RF packet was sent.

The new record is `/var/lib/dxberry/local-patches/program-uncertainty.json`.
`browser-audio-lifecycle.json` marks its binary as superseded while retaining
its history. The `.3` binary and previous manifest remain at
`/root/dxb-program-uncertainty-ec82410`. A rollback restores `dxberryd.before` to
the console binary and `browser-audio-lifecycle.before.json` to its original
manifest path atomically, restarts only the console, verifies `.3` and its
original `6d70b87ecf2f8e2ab10455ca5d446384558d102ad033d12a10272a366be382aa`
hash, then retires the `.4` marker after verification. It does not
restore or overwrite unrelated configuration. Keep every backup copy.

## Verification and maintenance composition

New regressions fail against the previous Go/shell implementations. They verify
that malformed/mismatched screen responses cannot free a held radio, owner
replacement cannot inherit either an old free answer or the wrong held label,
claim/takeover stay blocked for an unanswered replacement, and a verified
stopped replacement releases the hold. The full CLI response remains accepted.
Shell fixtures cover successful manager reads, deactivation, unknown states,
failed reads and refusal to infer inactivity from a second failed query.

Development and isolated maintenance source each passed all-package Go vet and
race tests in Docker. The maintenance source passed all ten existing Chromium
audio/PTT/STOP/timeout/disconnect/touch checks with simulated hardware. Repository
ShellCheck and the build-tree check passed in Docker. These are software checks,
not evidence of physical keying or audio quality. The CI-required Staticcheck
pass remains outstanding because its tool is unavailable in the cached image.
All 918 shell tests passed in a writable disposable source copy. The initial
read-only suite could not run the two build cases that deliberately move the
placeholder service binary; that harness failure was corrected before the
complete passing run. Go checks run separately from the shell suite.

The candidate console is `0.5.0-rc4+idle-reporting.2`, composed from the installed
maintenance source (`50aa825`, the status patches from `3817fa9`/`c51870e`, and
only the STOP selector fix from `7c75ab9`) plus this ownership change. Only
`bin/dxberryd` and `lib/screen.sh` are deployment files. Native WSJT-X and the new
Pat status client remain excluded. The ARM64 console requires the existing
`libopus.so.0`, `libpam.so.0` and `libc.so.6`.

| File | SHA-256 |
| --- | --- |
| Candidate console | `877622daed2cf5e20c1d4ebce854dbfe80105f93ce3124534a08a08628f90c91` |
| Candidate screen library | `6c8ce10f720c50fef660ab65984849e9ede66cd4bd8a4c4ddececb8e1925a644` |
| Required installed console | `3556d9c6672b771026f417d3957416dc96e276f21be9a945602818eb50d995c7` |
| Required installed screen library | `8334deeab493c9957fec9e75629d63998f0754ca7aac56bd4118ce7bb6e6aee5` |

The installer verifies these hashes and the installed version, takes the update,
configuration and radio-record locks, and requires the current empty registered
radio inventory. It retains both old files and the old maintenance manifest,
publishes files atomically with synchronization, and restarts only the console.
Post-install health/version and unchanged Graywolf settings/process, mixer,
radio/TX and network checks must pass. Any failed installation attempts rollback
and retains its backup; a rollback attempt must still have its outcome checked.
The manual Graywolf channel stays outside console ownership and is not rewritten.

## Installed maintenance

Installed at `2026-11-20T18:29:38Z` from
`68c5257d5299f8750eab5bb89b308ba06ae3ea1d`. The complete compressed transfer and
decompressed binary hashes matched before installation. Each of 145 transfer
chunks was sent sequentially and verified before assembly. The installed console
and screen-library hashes matched the candidate values above afterward.

The new record is `/var/lib/dxberry/local-patches/program-ownership.json`;
`idle-reporting.json` marks its binary as superseded by that record while
retaining its earlier source/history. Rollback files and the prior idle manifest
are retained at `/root/dxb-program-ownership-68c5257`.

HTTP and certificate-verified HTTPS health checks returned the new version.
Graywolf, nginx, Tailscale, netwatch and logbridge kept their process identities;
only the console restarted. The selected Graywolf configuration tables, CODEC
mixer, radio/transmit marker, reporting helpers and network configuration hashes
matched before and after. The Pi still had no registered DXBerry radios.
At `18:29:49Z`, all five core services were active, no units were failed, throttle
flags were `0x0`, and both PCM streams were RUNNING. The boot-scoped capture
report still contained the same earlier 350 input POLLERR records, with no new
logged burst. None of these checks closes the intermittent capture fault.

To roll back this maintenance, stop the console and atomically restore both
`dxberryd.before` and `screen.sh.before` from that backup, with their retained
modes, then restart and verify version `.1` and the prior file hashes. Restore
`idle-reporting.before.json` to its original manifest path and retire the new
ownership manifest after verification. Keep all backup copies. Restoring just
one of the two files is not the complete rollback. Do not change radio/mixer
settings or restart the modem for this console rollback.

Evidence is retained at `/workspace/dxberry-program-ownership-tests/` and
`/workspace/dxberry-owner-maintenance-tests/`, including `full-shell-writable.log`
and the current ten-check `browser-final.log`, plus `install-result.json` and
`post-health.json`. No stable release or new image is established by this
maintenance installation.
