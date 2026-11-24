# Graywolf capture reliability: current evidence

Latest follow-up: the November 23 metadata report retains 1,000 input POLLERR
records in three short logged clusters, with older records truncated. These
predate the `.3` console installation and do not represent 1,000 audio gaps.
The modem/recovery fault remains open; see the dated follow-up below.

Reception is working: the correctly tuned station decoded the owner's known
packet. This investigation concerns intermittent audio-stream errors, not a
claim that a quiet rural APRS channel has failed. The initial observations were
passive. The later controlled comparison temporarily muted playback and changed
the channel's output assignment, then restored the original settings. No RF test
transmission or raw audio export was performed.

## Configuration and observations

- Graywolf package: 0.14.14, arm64; package verification reported no changes.
- Kernel: 6.18.60+rpt-rpi-v8. Current throttle/under-voltage flags are clear.
- Interface: Texas Instruments PCM2906C, USB ID `08bb:29c6`, ALSA `CODEC`.
- Capture and playback hardware parameters: S16_LE, mono, 48,000 Hz, period 241
  frames, buffer 482 frames (about 5.02 ms and 10.04 ms respectively).
- A ten-minute journal slice contained 3,000 input `POLLERR` lines; a later
  three-minute slice contained 900 input and zero output error lines. These are
  logged counts, not an exact error rate: service logging is rate-limited.
- A passive 30.24-second observation sampled capture and playback every 100 ms.
  Both were RUNNING in all 300 samples, neither trigger timestamp changed, and
  the matching journal interval contained no logged POLLERR or rebuild notices.
  This establishes a stable sampled interval, not gap-free audio or long-term
  recovery; sampling and rate-limited logs can miss faults.
- The reported process/thread CPU figures attribute most usage to the modem's
  main thread. Total modem CPU is not evidence that its input polling thread
  alone is spinning.

The earlier receive-only comparison measured a 90-second Graywolf recording at
88.698 seconds versus a full 90.000-second raw ALSA recording. Those off-frequency
recordings did not establish a decoding defect. The failed larger-buffer trial
also remains evidence; do not repeat it as an untested proposed fix.

## Five-minute passive observation

From `2026-11-18T02:24:45Z` to `02:29:45Z`, 3,000 samples at 100 ms spacing
showed five changes to a nonzero RUNNING capture trigger timestamp. Successive
new triggers were separated by 61.977, 62.349, 61.599 and 62.105 seconds.
Capture states included XRUN, closed, PREPARED and OPEN between running periods.
Playback stayed RUNNING in all 3,000 samples with no trigger changes.

| Stream | RUNNING samples | Other samples | New RUNNING triggers |
| --- | --- | --- | --- |
| Capture | 2,985 | 5 XRUN, 8 closed, 1 PREPARED, 1 OPEN | 5 |
| Playback | 3,000 | 0 | 0 |

The matching journal window contained 1,500 input POLLERR lines and zero output
POLLERR lines. No disabled-endpoint kernel event was logged in that window.
Thus the capture restarts were observed without one of those kernel messages;
the earlier USB-event association is not a complete explanation of the fault.

This establishes recurring input overruns/restarts during this observation,
independently of RF traffic. It does not measure exact lost audio or packets:
state changes can occur between samples and logging is capped. The raw count of
trigger transitions also includes intermediate zero/reset values; the five
restarts above count only changed nonzero timestamps on RUNNING streams.

Observation metadata remains on the Pi at
`/root/dxb-capture-soak-20261118-022445/`; it contains no audio. All five core
services remained active, and no radio or mixer setting was changed.

## Controlled isolation

Two 180-second profiles used the same capture configuration. Capture-only kept
playback closed in all 1,800 samples but still produced input XRUN/closed states
and 600 logged POLLERR lines. Normal duplex kept playback running throughout
and also produced input XRUN/closed states and 600 logged errors. Playback is
therefore not required for the failure. The test kept the CODEC muted and used
automatic cleanup to verify the original configuration and mixer were restored.

Passive process correlation then found status-collection bursts about 62 seconds
apart. Two sampled XRUNs occurred at the same 100 ms samples as burst starts.
The console schedules idle collections 60 seconds after completion, and a full
report takes about two seconds on this Pi.

With only `dxberryd` paused for 180 seconds, capture stayed RUNNING in all 1,800
samples with no new trigger. Graywolf kept the same process. On automatic console
restart, capture closed and restarted within about 0.3 seconds. This strongly
implicates the console's work rather than packet availability.

Each of the nine report sections was then invoked individually with the console
paused: none changed the capture trigger. Two normal full parallel reports each
caused a restart. Two full reports run with `nice -n 10` did not; elapsed report
times rose from about 1.97 seconds to 2.67–2.74 seconds. This supports scheduling
contention from parallel reporting as a contributor.

## Reporting changes and validation

Short individual trials did not predict the heavier workload. The comparison
keeps normal console polling active, adds eight full reports at 30-second
intervals, and samples capture state and polling-process priority for four
minutes. All eight reports returned all nine sections successfully in each run.

| Reporting policy | RUNNING / 2,400 samples | Other samples | New RUNNING triggers | Logged input POLLERR |
| --- | --- | --- | --- | --- |
| Lower priority only (`48b1673`) | 2,257 | 138 closed, 1 SETUP, 1 PREPARED, 3 XRUN | 7 | 900 |
| Early lower priority and serial sections (`2f59280`) | 2,379 | 19 closed, 1 SETUP, 1 XRUN | 4 | 600 |
| Confined, early lower priority and serial sections (`520b257`), first run | 2,398 | 1 XRUN, 1 closed | 1 | 300 |
| Same confined policy, matched repeat | 2,400 | 0 | 0 | 0 |

The serialized version improved this observation but did not eliminate the fault.
A separate five-minute observation with normal polling and a lighter sampler
still found one event: 2,998 RUNNING, one XRUN and one closed sample, with 300
logged input errors. Playback stayed RUNNING throughout. These are sampled
outcomes, not exact lost-audio or packet counts; the trials are not randomized
and the journal is rate-limited.

The next controlled comparison paused only the console, left Graywolf and RF
settings unchanged, and invoked 12 full serialized, lower-priority reports under
each CPU policy. Five unrestricted reports changed the capture trigger; none of
the 12 reports confined to CPU 3 did. Every report succeeded. Unrestricted reports
took about 5.9–6.1 seconds; confined reports took about 4.08–4.13 seconds. The
console returned automatically afterward. This supports testing confinement; it
does not establish a general fix.

The standalone command now makes a best-effort restriction to the last CPU in
its inherited allowed set before loading libraries (`520b257`). It still lowers
priority by +10 and collects outer sections serially. Only the reporting process
and its children are restricted. The calling shell, daemon, radio-control and
modem scheduling stay unchanged; a missing or denied affinity utility does not
prevent reporting. Existing CPU restrictions are never expanded.

All 875 shell regressions, repository-wide shell syntax and ShellCheck, and
the build-tree check passed in containers. New tests cover restriction before library loading, an already
restricted caller, missing/denied affinity support, and the unchanged sourcing
shell. The new behavior tests fail against the preceding entry point. No Go
source changed in this slice.

The entry point was installed without restarting services. Configuration,
console binary, status library and mixer checks matched before and after.
Rollback is retained at `/root/dxb-status-affinity-520b257`; the active patch
manifest is `status-cpu-affinity.json`. The serialized status library remains
active from `status-serialized-reporting.json`.

The first confined comparison ran from `2026-11-18T03:59:21.870Z` to
`04:03:21.882Z`. It still found one capture restart, at about 1.4 seconds, with
300 logged input errors. All eight reports succeeded in 4.09–4.87 seconds, and
the daemon process stayed unchanged. This is a reduction in observed disruption,
not an elimination of the fault. The matched repeat from
`2026-11-18T04:04:34.700Z` to `04:08:34.711Z` was clean: all 2,400 samples were
RUNNING, with no trigger changes and no logged input POLLERR. All eight reports
returned all nine sections in 4.05–4.20 seconds, with the daemon unchanged.
The first run's interruption remains part of the evidence; it is not discarded
as warmup. These two observations support a mitigation under the tested load,
not a general or long-term reliability claim.

A subsequent effective-policy observation confirmed the reporting process
settled at CPU 3, nice 10, while the daemon stayed at CPUs 0–3, nice 0. The
report succeeded with all nine sections. One initial process sample preceded
the wrapper's adjustment; confinement is best effort after process startup,
not an assertion that every instruction from process creation is restricted.

Metadata locations on the Pi (no audio):

- `/root/dxb-capture-compare-20261118-023926/`: controlled isolation and CPU pilot.
- `/root/dxb-status-priority-stage/validation/`: priority-only failed validation.
- `/root/dxb-status-serial-stage/validation/`: serialization stress result.
- `/root/dxb-status-serial-stage/passive-validation/`: lighter normal-poll result.
- `/root/dxb-status-affinity-stage/validation/`: first confined-report comparison
  and effective-policy observation.
- `/root/dxb-status-affinity-stage/repeat-validation/`: matched repeat.

The controlled diagnostic services have completed and restored the console,
original radio configuration and mixer. No experimental receive-only policy
remains active.

## Longer normal-poll observation

With the confined reporter still active, a 20-minute observation from
`2026-11-18T04:11:04.843Z` to `04:31:04.843Z` sampled both streams every 100 ms
without adding manual reports or changing the radio. Capture had 11,998 RUNNING,
one XRUN and one closed sample. It changed to one new nonzero RUNNING trigger
at about 1,124.6 seconds. Playback stayed RUNNING in all 12,000 samples with no
trigger change. The journal contained 300 logged input errors and zero output
errors. It reported no rebuild lines; the sampled stream transition is still
independent evidence of an interruption.

The sampler's raw capture trigger count is two because it includes an
intermediate reset timestamp. The independent analysis counts only changed
nonzero timestamps on RUNNING streams, yielding one restart. Neither sample
count nor capped error logging measures exact lost audio or packets.

This observation retains the fault as open. The installed reporting changes
reduce the observed disruption under the tested workloads, but neither the clean
four-minute repeat nor a mostly RUNNING longer interval establishes complete
reliability. The metadata and trigger analysis are retained at
`/root/dxb-status-affinity-stage/long-passive-validation/`; no audio was captured.
All core services stayed healthy afterward, configuration and mixer checks still
matched, power/throttle flags were clear, and RF transmit remained enabled.

## Later read-only follow-up

At `2026-11-18T12:34:32Z`, the capture PCM was RUNNING. A journal count from
`04:31:05Z` through that read found 6,224 input POLLERR lines and zero output
POLLERR lines. This confirms further logged errors after the earlier observation;
it is not a continuous capture-state sample, an exact count of interruptions,
or a measurement of lost audio or packets. Logging remains rate-limited.

All five core services were active, no units were failed, and throttle flags
were `0x0`. The preceding two-hour kernel journal had no matching USB, sound,
reset, disconnect or voltage entries. Neither a running PCM nor the absence of
those kernel messages closes the fault. No report workload, audio recording,
configuration change or service restart was introduced for this follow-up.

## Follow-up after idle reporting

The console-only idle-reporting update was installed on 2026-11-20 at
`16:48:22Z`. At `16:56:04Z`, the journal since installation contained zero
input and zero output POLLERR lines, capture was RUNNING at that read, and all
five core services were active. This is an initial interval of about eight
minutes, not a continuous PCM-state observation or a long-term reliability
result. It does not establish gap-free audio or exact packet reception.

The earlier longer observation still contains a restart after about nineteen
minutes, so this shorter clean interval does not close the fault. No capture
load, test packet, recording or radio-setting change was added for this read.

The longer read at `17:14:59Z` found 154 input and 146 output POLLERR lines,
all in the `17:08Z` minute. A subsequent boot-scoped review changed the
interpretation: all 300 records belong to the previous boot
`b300fc8882284abf93066eb6ce1f82b5`, across about 36 ms, and coincide with its
kernel message `usb 1-1.4: USB disconnect, device number 3` at `17:08:34Z`.
They are device-removal-associated errors, not evidence of a new spontaneous
capture failure after the idle update. Counts still do not measure independent
gaps or lost audio.

The earlier kernel query used `journalctl -k`, which defaults to the current
boot and missed that prior-boot notice. Wall-clock filtering alone also mixed
records across the reboot. Current boot `cb8dd11deb2c46a6a9da311ce9a0d5b0` started
around `17:09:09Z` by uptime; its initial wall clock was about nine minutes slow.
Chrony recorded a `553.716630` second step at `17:09:33Z`, so displayed timestamps
from the two boots overlap. No orderly-shutdown marker was found in the selected
old journal search; that does not identify who or what caused the restart.

At `17:35:20Z`, the current boot had zero logged input/output POLLERR records.
Both streams had been RUNNING at the preceding read. This is log evidence with
rate limits and occasional state snapshots, not continuous gap-free audio.
The independently observed November 18 capture restarts remain unresolved.
Graywolf retained its process across each console installation; that statement
does not span the intervening Pi reboot. No reboot was requested by either
maintenance installer.

Use the [boot-scoped metadata report](capture-report.md) for subsequent checks.
It separates historical journal selection from current PCM state. In the live
metadata review, ALSA owner task 981 was the `audio-soundcard` thread of process
950, not a process leader. Its `cpal_alsa_in` and `cpal_alsa_out` workers were
ordinary timesharing threads at nice 0. The service had `LimitRTPRIO=0`; no
scheduling or capability change was made. These observations and the small
buffer justify further investigation, not an automatic real-time-priority fix.

### A distinct current-boot overrun

The current-boot log remained clear through `17:35:20Z`, but the first operational
boot-scoped report during its installation at `17:43:29Z` caught capture in XRUN
while playback was RUNNING. It included 350 input and zero output POLLERR records
in current boot `cb8dd11deb2c46a6a9da311ce9a0d5b0`, at `17:43:28Z`, across
27,865 microseconds of log timestamps. The selected kernel journal contained no
entries in the surrounding minute. Both streams were RUNNING again at `17:45:14Z`.

| Identified burst | Boot | Logged input / output | Independent context |
| --- | --- | --- | --- |
| `17:08:34Z` | Previous | 154 / 146 | CODEC USB-disconnect notice; Pi subsequently restarted |
| `17:43:28Z` | Current | 350 / 0 | Capture XRUN in the live snapshot; no kernel entry in the surrounding minute |

These are the identified bursts, not whole-boot totals or counts of lost audio.
The newer observation establishes a real input overrun after the idle-reporting
mitigation, separately from the earlier removal event. It occurred during
diagnostic installation/read activity; that timing does not isolate which work
or hardware condition caused it. No real-time promotion, buffer change, modem
replacement, RF test or recording was introduced. The retained first report is
`/root/dxb-capture-report-60b8e3a/current-report.json`.

## USB endpoint evidence

The kernel logged two disabled-endpoint transfer events at
`2026-11-18T00:35:34Z` and `2026-11-18T01:03:29Z`, both for slot 3, endpoint index
3. Each surrounding ten-second Graywolf journal window contained 300 input
stream errors. This timing is an association, not a causal diagnosis.

The live xHCI debug mapping identifies slot 03 as USB path `1-1.4`, the CODEC.
The device descriptors identify audio playback as USB EP 2 OUT and capture as
USB EP 4 IN. Linux 6.18 prints a zero-based endpoint index in this error; the
debug context `ep03` is the playback endpoint. The capture context is `ep08`.
Do not describe this particular kernel message as a capture-endpoint fault.

The event could be related to stream teardown or USB duplex behavior. It does
not by itself prove a bad cable, power supply, receiver, or kernel regression.
Clear current power flags also do not rule out every USB or power problem.

## Source comparison

The Graywolf v0.14.14 source already prefers native I16 over a default F32
capture/playback format on these USB codecs. Therefore the documented historical
F32 fix is not, by itself, a new fix to apply here. Physical ALSA hardware
parameters do not independently prove every format inside a converting plug PCM.

The pinned CPAL 0.17.3 ALSA worker reports a backend error when descriptor revents
contains ERR, before its later availability/EPIPE handling. Graywolf marks the
input stream failed and rebuilds it on its holding thread with a bounded backoff.
This explains the error/rebuild path but does not identify why ALSA raised ERR.
A single underlying fault can generate many error callbacks before teardown;
do not treat the number of log lines as the number of independent audio gaps.

Useful diagnostic additions upstream would report negotiated stream format,
PCM state, descriptor flags and recovery/backoff timing around this error. A
change to ALSA recovery behavior needs its own reproduction and validation.

### Upstream recovery fix identified on November 20

There is now a specific upstream candidate, rather than a proposed local modem
rewrite. CPAL [PR 1122](https://github.com/RustAudio/cpal/pull/1122), merged as
`5931fa2910d81020266eae5ce77bc9e96934851b`, changes the ALSA worker to handle
descriptor POLLERR as a pending xrun and perform recovery. It also restarts
capture after preparing an overrun stream and handles device removal separately.
Those changes are absent from the installed CPAL 0.17.3 path described above.
The [0.18.0 release](https://github.com/RustAudio/cpal/releases/tag/v0.18.0)
documents the capture-overrun fix; the latest CPAL release checked was 0.18.2.

Graywolf has already integrated CPAL 0.18.2 in upstream commit
[`4978244d2ea61bf5635e882ad650c46f873bd9ca`](https://github.com/chrissnell/graywolf/commit/4978244d2ea61bf5635e882ad650c46f873bd9ca).
It adapts the changed APIs and device-name lookup, preserves ALSA PCM identifiers,
and avoids rebuilding for a single recovered xrun. Its new error policy still
escalates more than ten xruns within a one-second policy window to a rebuild.
The related [issue 628 discussion](https://github.com/chrissnell/graywolf/issues/628#issuecomment-6050930140)
also records upstream build checks and a snapshot test plan. Those are upstream
reports, not locally reproduced tests or acceptance on this station.

Issue 628's original timestamp-overflow diagnosis concerns 32-bit ARM and does
not apply to this arm64 Pi. The generally applicable ALSA recovery changes are
the reason to investigate this revision. Likewise, the closed
[Pi Zero/HiFiBerry report](https://github.com/RustAudio/cpal/issues/1157) involves
different hardware, format and buffer choices. It supports reviewing the change
in default buffering after CPAL 0.16, not assuming its 1,024-frame choice fixes
this PCM2906C. The earlier failed local larger-buffer trial remains evidence.

As of this review, Graywolf's latest published release was still v0.14.14,
published October 1, with CPAL 0.17.3 in its lockfile. Development source contains
the migration, but there is no newer published release established by this check.
No snapshot, rebuilt modem, dependency override, buffer change or real-time
permission has been installed on the Pi.

The next software candidate should therefore use the upstream migration, with
its exact source/package digest and dependency versions recorded, rather than
changing only the CPAL version in the station package. Before deployment:

1. Verify the upstream package or build the pinned source in Docker, including
   the device-name/API migration and error policy. Check saved input/output
   identity compatibility against synthetic equivalents of this manual setup.
2. Exercise recoverable xrun, failed recovery, device removal, burst escalation
   and queued-output accounting in isolation. Fixture success establishes those
   branches only; it cannot establish this CODEC's delivery or RF behavior.
3. Retain the installed package and configuration/mixer snapshot for rollback.
   A live trial requires a planned modem interruption, identical radio/VOX/PTT
   settings, and independently measured capture continuity under normal use and
   matched reporting load. Do not use reduced log counts alone as acceptance:
   newer code can recover without emitting the old POLLERR message.

At `2026-11-20T17:59:30Z`, a subsequent manual report still contained the same
350 current-boot input POLLERR records and no output records, with both streams
RUNNING and all five core services active. This is another metadata snapshot,
not continuous capture acceptance. The first XRUN report remains retained.

After the separate program-ownership maintenance installation at `18:29:38Z`,
the `18:29:49Z` report still contained those same 350 input records, zero output
records and the same timestamp bounds in the same boot. Both streams were
RUNNING with the same owner process and negotiated parameters. This establishes
no new logged burst at that read, not continuous gap-free audio or a modem fix.
The first XRUN report remains unchanged on the Pi.

## November 23 follow-up

At `2026-11-23T01:49:55Z`, the installed metadata-only capture report selected
the same current boot, `cb8dd11deb2c46a6a9da311ce9a0d5b0`. Both CODEC streams
were RUNNING under the same modem process, with S16_LE/48 kHz and unchanged
241-frame periods/482-frame buffers. The isolated `.3` console was running,
all six checked services were active, and TX remained enabled and unmuted.
That console update changes browser-audio bookkeeping and radio-poller startup
ordering; it does not change Graywolf or CPAL recovery.

The bounded journal result returned 1,001 records and considered the most recent
1,000: all input POLLERR, no output records in that retained slice. The result
is explicitly truncated. Its wall-time bounds are `2026-11-22T22:41:50.515900Z`
through `2026-11-23T01:47:15.858192Z`, with a complete monotonic span of
11,125.342292 seconds. This demonstrates additional logged errors since the
November 20 snapshot; neither 1,000 nor the retained span is a boot-total rate.

A bounded, same-boot timing projection of that fixed historical slice grouped
records only where neighboring monotonic timestamps were less than one second
apart. It retained these three logged clusters:

| First UTC | Last UTC | Retained records | Monotonic span |
| --- | --- | --- | --- |
| Nov 22 22:41:50.515900 | 22:41:50.556102 | 301 | 40.202 ms |
| Nov 23 00:17:01.875379 | 00:17:01.909846 | 350 | 34.467 ms |
| Nov 23 01:47:15.830927 | 01:47:15.858192 | 349 | 27.264 ms |

Only three boot-aligned one-second buckets contain those retained records;
the first cluster is clipped by the record limit. These are log groupings,
not three measured audio gaps, restart counts or loss-duration measurements.
Logging limits and missing older records still apply. The latest cluster
preceded `.3` installation at `01:47:16Z` and occurred during the maintenance
sequence; timing alone does not establish which activity caused it. This slice
provides no post-update continuity acceptance.
The live service reports a configured log-rate interval of one minute and
burst setting of 200; no logging limit was changed for this observation.

A separate bounded filtered kernel query returned nonzero. Its result is
unavailable, not evidence that zero USB/kernel events occurred. No raw journal
messages, audio or packet contents were exported. The retained timing summary
is `/root/dxb-capture-followup-20261123-014955/timing.json`; local evidence is
at `/workspace/dxberry-capture-followup-20261123/`. The older first XRUN report
remains untouched. No setting, modem, buffer, thread priority or RF operation
was changed by these reads.

Graywolf release metadata was rechecked on November 23: the latest published
release remains v0.14.14, dated October 1. The upstream CPAL 0.18.2 migration
identified above remains an unverified development candidate for this station.

## Next controlled comparison

1. Observe the confined, lower-priority version over longer normal operation,
   including actual browser use. Sampling cannot see transitions between reads, and capped logs can hide
   errors. Do not infer exact lost packets from this observation.
2. If loss persists, compare longer capture delivery under matched CPU/reporting
   load before changing sound-card settings or replacing the modem. The first
   capture-only/duplex comparison did not eliminate the fault.
3. If a remaining failure is tied to hardware USB operation, change one physical
   variable at a time with the owner: the CODEC's port, cable or powered hub.
   The Pi boots from separate USB media; never unplug the boot drive for this test.

The normal station remains configured for transmit after the owner's repair.
No upstream issue or diagnostic bundle has been submitted.

## Source references

- [Graywolf audio and recovery](https://github.com/chrissnell/graywolf/blob/v0.14.14/graywolf-modem/src/audio/soundcard.rs)
- [CPAL 0.17.3 ALSA worker](https://github.com/RustAudio/cpal/blob/v0.17.3/src/host/alsa/mod.rs)
- [Linux 6.18 xHCI transfer events](https://github.com/torvalds/linux/blob/v6.18/drivers/usb/host/xhci-ring.c)
