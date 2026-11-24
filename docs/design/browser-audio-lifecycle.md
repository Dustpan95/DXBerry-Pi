# Browser audio peer cleanup

These fixes remain in the current `.4` console. The `.3` installation below is
the retained parent of the later [ownership maintenance](program-ownership.md).

An audio peer that ended unexpectedly could leave its signaling socket counted
against the login's four-peer limit. A browser that did not close that socket
could therefore prevent a fourth healthy tab from starting audio. The existing
page normally closes signaling after an audio-loss notice, but quota cleanup
must not depend on that client action.

Each offer now reserves quota with a distinct in-memory generation. The bridge
keeps that generation with the actual Pion peer and includes it in a dedicated
loss notification. Signaling releases quota only when the socket and exact
offer still own that slot. It does not close a peer by tab name in response to
loss: the bridge already removed the lost peer, and a replacement may exist.
The existing tab-only loss notification used by the PTT guard is preserved.

Connect and close operations are serialized for each tab, without holding the
global quota lock across bridge work or delaying other tabs. Pending offers
still count toward the limit. A failed offer frees its reservation and can be
retried on the same socket. A confirmed peer loss ends that socket's audio
lifecycle; reconnect opens a fresh socket, as the existing browser already
does. This prevents a delayed loss notice racing a new answer on that socket.
Offers for a replacement peer receive a new generation, so duplicate or delayed
notifications for preceding offers cannot release its quota or report it lost.

## Software evidence

On November 23, a Docker regression reproduced the old failure: with the lost
peer's socket left open, the fourth healthy tab was refused. The fixed complete
development tree passes Go vet and the race suite with networking disabled.
New checks use real loopback WebSockets with a synthetic bridge to cover quota
release without socket close, loss notice fields and duplicates, a delayed old
loss after replacement on the same or a new socket, loss during Connect, and
overlapping offers while other tabs connect. They also verify pending-offer
quota, late-candidate refusal and recovery on a fresh socket.

A separate actual Pion bridge check delays a loss notification, connects a new
peer while delivery is held, then verifies that the old notification retains
its original offer identity and the replacement remains registered. Audio is
generated in Docker through fixture pipes; no radio or sound device is used.
Evidence is retained at `/workspace/dxberry-audio-loss-tests/`.

## Maintenance scope

The maintenance console `0.5.0-rc4+idle-reporting.3` is built from the isolated installed
`.2` maintenance source with only the bridge/signaling change and regression
tests copied in, plus the existing radio-poller subscription-order fix from
`52a4601`. The first maintenance race run reproduced that older startup bug:
the poller's first update could be published before forwarding subscribed.
Subscription now precedes polling and discovery; native program lifecycle
changes from that development commit are not copied. The preceding production
bridge/signaling sources were
byte-for-byte identical to the development parent. Native WSJT-X and Pat status
work remains excluded. The maintenance tree passes its own Go vet and race
suite, all ten real-Chromium audio/PTT checks and ARM64 compilation. The binary
is AArch64 and retains Opus, PAM and libc as its shared-library dependencies.
Its SHA-256 is
`6d70b87ecf2f8e2ab10455ca5d446384558d102ad033d12a10272a366be382aa`.
Build VCS stamping is disabled for the isolated repository mount.

## Pi installation

Installed through DockerPC at `2026-11-23T01:47:16Z`, with source patch commit
`ce6b2cc921dcd744856fe920cd9c5557464144d8`. All 192 transfer chunks, the complete
archive and the executable matched their checksums before installation.
The installer held update/configuration/radio-record locks and required the
known `.2` binary and empty registered-radio inventory. It retained the working
binary and settings snapshots at `/root/dxb-audio-lifecycle-ce6b2cc`, replaced
only the console executable atomically, and restarted only `dxberryd`.

HTTP and certificate-verified HTTPS reported `.3`. Graywolf, nginx, Tailscale,
netwatch and logbridge kept their process identities. Graywolf audio/channel/
PTT/beacon/gating tables, mixer output, radio/TX and network file checksums
matched before and after. Independent post-install health at `01:47:37Z`
confirmed all six services active, both CODEC streams RUNNING, TX enabled and
playback unmuted. No radio was registered and no test RF was transmitted.

The active manifest is
`/var/lib/dxberry/local-patches/browser-audio-lifecycle.json`;
`program-ownership.json` records that its console binary was superseded.
Earlier patch manifests and rollback copies remain intact. The rollback copy
`dxberryd.before` restores `.2`; restore it atomically with the update and
configuration locks held, restart only the console, restore
`program-ownership.before.json`, retain the new patch manifest in the rollback
directory and remove its active copy, then verify HTTP/HTTPS health. The automatic
failure path uses those same retained copies. No rollback exercise is claimed.

These software checks do not establish physical audio quality, RF behavior,
phone/Safari permissions or resolution of the separate Graywolf capture fault.
The CI Staticcheck result remains unavailable with the tool absent from the
cached environment; no complete release-check claim is made.
