# Idle station reporting

The carried-over idle-mode idea starts with optional full-station reporting.
Before this change, the console collected a full `dxberry-status --json` report
every minute even with no connected browser. Earlier capture observations associated report
bursts with some input overruns. Removing unused work is useful independently
of whether it resolves that hardware-sensitive fault; this change does not
claim a capture fix.

The current maintenance console is `0.5.0-rc4+idle-reporting.4`, retaining this
scheduler and the earlier read-only/STOP fixes plus
[program ownership hardening](program-ownership.md) and
[browser audio lifecycle maintenance](browser-audio-lifecycle.md). The installation records
below describe the earlier scheduler/contrast increments. Their binaries and
rollback copies remain retained; `browser-audio-lifecycle.json` now identifies the
active console binary.

## Behavior

- Keep one startup report to populate the cache.
- While an events client is connected, retain the existing 30-second cadence.
  A client that stays connected for audio or transmitter ownership still counts.
- With no clients, stop the periodic full-report timer. Finish an in-flight
  report rather than canceling it on a tab close.
- Reopening events wakes the loop. If the last attempt is already 30 seconds
  old, refresh immediately; otherwise retain its ordinary deadline.
- A one-shot authenticated `GET /api/status` immediately returns the cache and
  requests a background refresh when the last attempt is at least 30 seconds
  old. Coalesce requests during an in-flight collection. Failed attempts also
  obey this interval, so repeated reads cannot create a retry loop.
- Explicit state-changing CLI calls still request a debounced refresh. They are
  independent of client demand and remain effective while the browser is closed.
- Cached snapshots become stale after 90 seconds, or immediately after a failed
  report. Neither opening nor closing a client changes the cache's timestamp.

## Boundary

Only the full-station report scheduler changes. Radio polling, CAT/PTT, transmit
guards, audio, program ownership/lifecycle, native observation and update/job
schedules remain independent. A report already running finishes under its
existing deadline. This is not a process sleep, CPU governor, modem stop or
radio registration change. Manual reporting remains available.

Verify long idle intervals, reconnection deadlines, active-client cadence,
one-shot reads, failed-attempt throttling, in-flight demand coalescing, mutation
refresh and shutdown with fake time and race detection in Docker. A separate
deployment candidate must preserve the installed maintenance baseline and have
an ARM64 console build before Pi installation. Preserve backups and existing
radio/TX settings. Local software evidence does not close physical capture or
browser acceptance, and the unaccepted native WSJT-X work stays inactive.

## Local software evidence

All-package Go vet and race tests passed for both the development tree and the
maintenance source based on installed commit `50aa825`, with only the scheduler
patch added. Focused idle/read/reconnect/mutation checks passed 25 repetitions
under race detection. Fake-time coverage includes a day without clients, no idle
timer, aged authenticated HTTP reads, throttled failures, 1,000 coalesced reads
during an in-flight collection, and a separately retained mutation refresh.

The maintenance source passed all nine isolated Chromium audio/PTT checks,
including STOP, timeout, audio-close and tab-close unkeying. The development tree
passed all eleven native-status browser groups. These use synthetic devices
inside Docker, not a physical radio.

Native and ARM64 console builds passed; the development ARM64 settings helper
also built. The maintenance ARM64 binary links the existing `libopus.so.0`,
`libpam.so.0` and `libc.so.6`. Building the development console does not establish
actual WSJT-X/systemd compatibility or authorize its native activation. The
maintenance binary uses `0.5.0-rc4+idle-reporting` and preserves the installed
Pat and browser-audio fixes without including the new native integration.

## Pi maintenance installation

Installed on 2026-11-20 as `0.5.0-rc4+idle-reporting`. The reproducible
maintenance source is commit `50aa825` plus the `station/internal/status` diff
from `3817fa9^` to `3817fa9`; it includes neither native WSJT-X hooks nor the
new native display. The installed ARM64 console SHA-256 is
`23ecec33470bf5967a23c36bb611b4269169d360a056575a7f02bf26323ebf2d`.

The transfer and binary checksums matched. The installer retained the previous
binary and snapshots at `/root/dxb-idle-reporting-3817fa9`, held the update and
configuration locks, and restarted only `dxberryd`. Operational HTTP and
certificate-verified HTTPS version checks returned the new version. Graywolf,
nginx, Tailscale, netwatch and logbridge retained their process identities.
Graywolf channel/audio/PTT/beacon/gating configuration, radio/network files,
transmit-policy record and CODEC mixer matched before and after. All five core
services were active, no services failed and throttle flags were `0x0`.

The new record is `/var/lib/dxberry/local-patches/idle-reporting.json`. To roll
back, stop the console, atomically restore `dxberryd.before` from the retained
directory to `/opt/dxberry/bin/dxberryd`, restart the console and verify its old
version. This rollback does not require changing radio configuration or
restarting Graywolf. Keep the snapshots and reconcile the manifest afterward.

The earlier reporting priority/serialization/CPU-affinity library patches remain
installed. Capture reliability still needs longer natural-use evidence; no new
capture load, RF transmission or raw-audio export was introduced by this update.

## Service-activity read follow-up

The authenticated CLI bridge's `systemctl is-active` query was incorrectly
classified as a mutation. It could therefore queue a full station report after
an otherwise read-only service check. The classifier now excludes that exact
query while keeping start, stop, restart and reload refreshes intact. This
changes refresh classification, not command permissions or service execution.

A regression using the production HTTP/CLI handler and an inert process runner
fails before the correction. Afterward, twenty health queries still return their
answers without scheduling a report, and each service mutation publishes a new
cache. Status/CLI vet and race tests passed, followed by 25 race-enabled repeats
of this regression. Fake time advances only after cache publication, avoiding
a race between entering the runner and finishing a report.

This follow-up is included in the installed `0.5.0-rc4+idle-reporting.1` build
described below.

## Installed maintenance follow-up

The follow-up isolated console build is `0.5.0-rc4+idle-reporting.1`. Its source is
the same `50aa825` baseline, the status-package patches from `3817fa9` and
`c51870e`, and only the `.vfo-stop` → `.btn.vfo-stop` selector correction from
`7c75ab9`. No other native-status CSS, JavaScript, settings helper or lifecycle
hook is included. In particular, the older hardcoded font stack stays intact;
copying the complete newer stylesheet would require unrelated token changes.

The installed baseline's light-theme STOP text was red on red: a real Chromium
check measured contrast `1.00` and failed before any simulated PTT action. The
new selector preserves the red button and gives its white label precedence over
the generic danger style. Both light and dark checks measure contrast `6.54`.
Before/after button screenshots were inspected. The shared browser audio/PTT
workflow now guards this behavior without depending on the native WSJT-X fixture.

All-package vet/race checks and the ARM64 build passed on the exact maintenance
source. Its ten browser checks passed, including the contrast check and the nine
existing receive/PTT/STOP/timeout/disconnect/touch checks. Installer syntax and
ShellCheck passed. The console SHA-256 is
`3556d9c6672b771026f417d3957416dc96e276f21be9a945602818eb50d995c7`.
This is scoped maintenance evidence; the separately documented legacy UI audit,
official design-format lint and native application/hardware acceptance gates
remain open. No stable release or new image follows from this candidate.

Installed at `2026-11-20T17:14:25Z`, with the prior console and patch manifest
retained at `/root/dxb-console-maintenance-b74b739`. The transfer preflight
caught missing and empty staged chunks before installation; those parts were
retransmitted and individually verified, then both complete archive and binary
hashes matched. The failed preflights never changed the live console.

The installer held the existing update/configuration locks, checked the expected
previous binary and empty registered-radio inventory, and restarted only the
console. HTTP and certificate-verified HTTPS returned the new version. Graywolf,
nginx, Tailscale, netwatch and logbridge kept their process identities. Graywolf
configuration, mixer, radio/network files and transmit-policy record matched
before/after. All five core services were active, no units failed, throttle flags
were `0x0`, and RF transmit remained enabled. The current record is
`/var/lib/dxberry/local-patches/idle-reporting.json`.

The same atomic binary restoration procedure applies using this newer rollback
directory; its `manifest.before.json` records the superseded maintenance build.
The capture fault remains open: a longer journal follow-up found input and
output errors before this second console installation, despite the clean first
eight-minute interval. See the [capture evidence](../diagnostics/graywolf-capture.md).
