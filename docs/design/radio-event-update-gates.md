# Radio event update barriers

Status: conservative refusal barriers for the experimental hotplug queue.
The queue remains off by default. These changes do not implement automatic
consumer retirement, retained-work recovery or production activation.

## Shared update admission

The public update worker already holds an exclusive `update-apply.lock` through
work, result publication and exit cleanup. Experimental queue reconciliation,
unit publication and producer-rule publication now acquire a fresh shared lease
on that same lock. They return 6 on contention and retain pending work.
Enqueue remains independent of update, record, apply and worker locks.

The shared helper validates the root-owned runtime directory and an empty,
single-link, mode-0600 regular lock file before and after acquisition. It checks
directory/inode continuity and lock metadata again after its fixed internal
callback. Callback state and return status remain in the calling shell; detected
coordination drift returns 6 without pretending any effects were undone.

A dynamically allocated descriptor avoids FD 4, which the logbook library uses.
The shared lease is deliberately inherited into changing children. An inheriting
child therefore continues excluding a public update after its parent dies.
The queue's separate exclusive worker descriptor is still closed in the apply
child. No inherited-descriptor shortcut bypasses fresh lease acquisition.
This does not prove that every descendant or manager job has finished, and it
does not authorize a radio operation or establish free-radio state.

The helper reports protocol 2. Reconciliation requires the matching `common.sh`
shared-lease helper; enqueue and the read-only protocol query remain standalone.
Default provisioning now also leaves the experimental manager unit fragments
uncopied, in addition to not enabling them. Previously enabled experimental
configuration is not silently removed by an unset opt-in flag.

## Whole-tree refusal guard

DXBerry update checks before release lookup/download and again after extracting
and verifying a candidate, before a tree swap. Rollback checks after validating
the previous tree; development also rechecks after its native release guard.
The isolated maintenance patch has no native/generation helper dependency.

Changing the tree returns 6 when any of the following remains:

- The experimental queue opt-in is on.
- A known reconcile service/path/timer fragment or drop-in path exists.
- The configured udev rule contains the queue producer, or the rule cannot be
  safely read as a root-owned, single-link regular file with an accepted mode.
- The private queue retains pending/processing markers, worker-lock history or
  unexpected state. Only an absent queue, an empty private queue, or its empty
  private `marks` directory is accepted; nothing is deleted by this guard.
- Any reconcile unit is loaded, has a job, or cannot be positively shown absent
  by the manager. Each query must succeed and contain exactly one
  `LoadState=not-found`, `ActiveState=inactive` and empty `Job=` property.
  Failed, incomplete, duplicate and unexpected replies are unknown, not absence.

Refusal leaves the live and previous trees unchanged. A late refusal removes
only the newly extracted temporary tree. The guard does not stop units, reload
the manager, clear queue history, or resume an interrupted consumer.

Cooperating opt-in publication cannot cross the public worker's exclusive
lease. External configuration writers, complete installed-unit/protocol binding,
manager lifecycle races and all descendant lifetimes remain outside this
bounded guarantee. Library fixture calls are not the public execution boundary.

## Verification and remaining acceptance

Ten focused Docker groups passed with real files, processes and kernel flock.
They cover unsafe lock objects, callback effects/results, parent death with an
inheriting child, update/worker contention and retry, blocked opt-in publication,
configured/pending/producer/history refusal, ambiguous manager replies, refusal
before download, a late configuration change and the legacy positive path.
The original nine-group regression run failed eight groups on the previous
implementation; its positive compatibility group already passed.

Initial full runs exposed three failures in older rollback fixtures: their
service-change assertions also counted the new read-only absence queries.
Those queries now have their own retained fixture log. All four focused
rollback/isolation groups passed after that correction; clean full runs are
required below. Production behavior was not changed to satisfy those assertions.

Manager replies, release downloads, provisioning and radio apply are fixtures.
No actual manager delivery or hardware acceptance follows from these checks.
Read-only DockerPC diagnostics confirmed the installed Pi baseline and the
absence-property tuple for all three queue units. No queue worker or radio
operation was executed on the Pi. Evidence is retained in
`/workspace/dxberry-radio-update-tests/`.

The full Docker suites passed 1,033 development and 945 isolated maintenance
shell tests, with zero failures. Subsequent lint found a reused array/scalar
variable name and an unresolved source path. A local variable rename and an
explicit source directive corrected them. All 19 queue/barrier groups then
passed in each final snapshot, followed by complete Bash syntax, ShellCheck
and tree checks in both. The full-suite and initial lint logs are retained
separately from those final focused/lint runs.

Package scope checks matched the pinned Pi baselines and preserved maintenance
feature exclusions. Docker installer checks verified atomic bytes/modes,
restoration after a replacement failure, and diagnostic retention when
restoration itself is incomplete. No production Go or UI code changed, so those
broader suites were not repeated.

Real systemd path/timer delivery, start limiting, cancellation, safe consumer
retirement, swap/failure recovery and retained-work migration remain required
before queue activation. See [retained hotplug requests](radio-event-queue.md).

## Pi maintenance installation

Source `2d1ade8e4466bcb253fce9ea98a25a14099c70f7` was installed through
DockerPC at `2026-11-23T17:15:54Z`. Only `lib/update.sh` was replaced: the
refusal helper and three maintenance tree-swap checkpoints extend the exact
installed baseline. The shared queue lease/helper/publication changes were
not installed or activated. No development generation dependency was added.

The original update library remains at
`/root/dxb-radio-update-2d1ade8/lib/update.sh`. Its SHA-256 is
`62e9be225cd94eb2a5c58d047ca8043ebbd5137c8b417d68e392403d0ba6e1ee`;
the installed library is
`5011382f2e52396761cc9ab361540e4cbbb08f0393c0e60c3e3391786d2d6289`.
The durable local patch record is
`/var/lib/dxberry/local-patches/radio-event-update-barrier.json`.
The six transfer pieces and 17,904-byte archive were verified before extraction
(SHA-256 `f53a6e43365e0458e0fd7f505a125461ee83c65908c5fd4b934a71e3b522394f`).

Installation held the existing update/settings/provisioning and operation
gates, checked the pinned file hash/mode and absence of active jobs, retained
and synced the original, then atomically replaced the library. No update job,
provisioning run, service restart, registration or radio operation was issued.
HTTP and verified HTTPS version diagnostics passed. Independent diagnostics
after installation and again after candidate staging verified the installed
and saved-original hashes, unchanged `.4` console, all seven service identities,
checked radio/TX/helper/network files, Graywolf configuration and mixer.
Both PCM streams remained RUNNING. This is maintenance verification, not a
Pi execution test of queue delivery, update/rollback or physical acceptance.

## Inactive protocol-2 candidate

The matching eight-file queue candidate was staged at
`2026-11-23T17:16:52.084818+00:00` under
`/root/dxb-radio-event-candidate-2d1ade8`. It includes the matching shared
`common.sh` dependency and no installer. Four transfer pieces, the 10,987-byte
archive, extracted file hashes, root ownership, modes and single-link types
were verified (SHA-256
`5649407f093b6ae5cba08e3c8319038f2c449a2637aa94e9d09f2101f792a1a3`).

Its `staging-record.json` confirms active candidate counterpart files were
unchanged, the new helper/templates were absent from the active tree, and all
three manager units remained positively absent. The earlier protocol-1
candidate remains retained separately. No placeholder binary was deployed;
the queue remains inactive pending the acceptance work above.
