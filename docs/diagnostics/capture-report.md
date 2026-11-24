# Boot-scoped capture report

Run these explicit, root-only reports when investigating audio-stream events:

```sh
sudo /opt/dxberry/bin/dxberry-status --json --capture
sudo /opt/dxberry/bin/dxberry-status --json --capture --previous-boot
```

The first command selects the current kernel boot ID. The second selects the
previous journal boot by its index and then queries that exact ID. It never
selects boots using wall-clock timestamps. Live PCM metadata always belongs to
the current boot and is labeled separately, even when the journal selection is
previous. An unavailable previous boot stays unavailable rather than falling
back to the current one.

This distinction matters after a restart with an initially incorrect clock:
timestamps from adjacent boots can overlap, and NTP can move wall time during
one boot. The event span uses monotonic journal timestamps within the selected
boot; wall-clock minima/maxima are context, not an elapsed-time measurement.
Missing timestamps remain unknown. Records from another boot are rejected and
counted separately.

The report includes only:

- Current PCM state, hardware format/rate/period/buffer metadata, and the opening
  task's ID, name and process ID. ALSA `owner_pid` can be a thread ID; its `Tgid`
  identifies the process leader. It is not necessarily the callback worker.
- Selected-boot counts of logged input/output/unclassified POLLERR records,
  timestamp completeness and bounds, and explicit history truncation.

It opens no sound device, records no audio, changes no settings or service,
and exports no raw journal messages, stderr, packet contents or credentials.
Journal input is projected through a fixed metadata whitelist. Temporary input
files are private and removed when the report exits.

The limits are 32 live PCM records, the newest 1,000 matching journal records,
a 2 MiB journal-input bound, a 128 KiB boot-list bound and five seconds per
journal command. An extra journal record detects truncation. Command failure,
timeout, oversized/malformed input and missing boot identity produce an explicit
unavailable reason instead of zero errors. Logged counts remain rate-limited;
they are not counts of independent gaps or lost packets. A current RUNNING state
does not establish uninterrupted capture between observations.

Capture diagnostics are an opt-in JSON mode, not a new default report part.
They cannot be combined with normal parts and are not added to the web CLI
allowlist. Ordinary background reports and the idle scheduler do no additional
collection. The command retains the existing lower-priority/CPU-affinity policy.

Software verification uses fake proc/journal sources in Docker, including
overlapping/backward wall time, separate boot identities, thread/process mapping,
bounded history/streams, unavailable sources, invalid JSON, redaction and CLI
mode isolation. Physical capture reliability remains a separate acceptance gate.

For a separate kernel review, use the report's explicit selected boot ID with
`journalctl --boot=BOOT_ID -k`. A bare `journalctl -k` defaults to the current
boot, even if a wall-clock `--since` range appears to include an older event.
This report does not infer a cause from error counts or identify which USB
device disconnected; correlate the selected boot, monotonic timing and device
mapping before drawing that conclusion.

The 917-test shell suite, repository ShellCheck and build-tree check passed in
Docker. After tightening malformed-record handling, all capture checks and lint
passed again. A CLI vet/race regression confirms that the new mode remains
outside the web allowlist. These are software checks with fixture metadata, not
physical capture acceptance.

Installed on the Pi at `2026-11-20T17:43:29Z` from `60b8e3a`. The status entry
point and new library were replaced atomically with file/directory synchronization;
the old entry point is retained at `/root/dxb-capture-report-60b8e3a`.
The record is `/var/lib/dxberry/local-patches/capture-diagnostics.json`.
No service restarted. Console/configuration hashes, all core process identities
and the CODEC mixer matched before/after; transmit settings remained unchanged.
At that installation the console version was `0.5.0-rc4+idle-reporting.1`.
The subsequent [ownership maintenance](../design/program-ownership.md) updates
the console to `.2` and preserves this report's entry point/library hashes.

The first operational report caught an input XRUN with playback RUNNING and
350 logged input errors in the current boot. It is retained as
`current-report.json` in that private rollback directory. This illustrates why
an available report and healthy services do not mean capture is reliable.
See the [capture investigation](graywolf-capture.md) for timing and limitations.

To roll back this command extension, restore the retained `dxberry-status.before`
atomically to `/opt/dxberry/bin/dxberry-status`, then retire the added library and
reconcile its patch record. Keep the backup; no console or modem restart is needed.
