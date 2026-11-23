# Update request and result binding

Status: implemented and installed as an isolated Pi maintenance patch. This
identifies an admitted update request for result presentation; preset authority
remains unavailable and the console stays on verified `.4`.

The former global result contained only an exit code and wall-clock time. A
delayed/manual worker could leave its result beside another request's job
record, and status could report that unrelated success. A job record changing
between the unit probe and response could also inherit the old unit's state.

## Request publication and worker identity

Admission now obtains 16 random bytes from the local kernel, validates a nonzero
32-character lowercase hexadecimal request ID, and refuses immediate reuse.
The unique unit name includes that ID, so same-clock submissions cannot reuse
the unit. Entropy or publication failure returns 6 before launch.

The version-2 request is atomically published and read back before calling
`systemd-run`. Both request ID and unit are passed explicitly to the worker
through `--setenv`. The worker validates their shape and exact kind/name/unit
relationship before entering work. Invalid or partial inherited metadata returns
6 without changing cache or result. Direct manual apply remains usable and
produces an explicitly unbound result.

The worker echoes version, request ID and unit with its exit and finish time.
Publication uses the existing atomic replacement helper plus exact readback.
Failure after body execution returns 8 and never invents a completion record.
This is process-visible publication, without a new media-durability guarantee.

The request remains recorded when launch fails or its outcome is uncertain.
An older result can be retained for diagnostics, but its different identity
cannot become the failed request's success. Request IDs identify cooperative
admission; they grant no control permission and do not establish the systemd
invocation identity of an arbitrary manual restart.

## Status reads

Status captures one job document, validates its unit spelling, probes that unit
and constructs the answer from the captured document. It reads the job again
before returning; changed or missing evidence produces `job: null` rather than
combining old unit evidence with new metadata.

Completed results require version 2, equal request ID and unit, an integer exit
code in 0–255 and a nonnegative integer finish time. A nonblocking shared lease
on the existing valid execution gate must also succeed: a worker that has
written its result but is still doing EXIT cleanup cannot appear completed.
Legacy, malformed, unrelated, missing or unavailable evidence yields the
existing `ended`/null-exit presentation. A running unit remains `running`.
The public job shape omits the redundant internal version/request-ID fields;
the existing interface already handles an ended job without a result.

These status documents do not provide the protected-path, effective installed
protocol, current manager/process and restoration evidence required by a preset
target verifier. Privileged/nonparticipating writers, arbitrary unit restarts,
and surviving descendants still require their own verified scope. No production
preset target is constructed by this change.

## Verification

Docker command/filesystem fixtures cover unrelated and legacy success records,
malformed values, same-clock requests, synchronous worker completion, entropy
and request-publication failures, invalid inherited metadata, result-publication
failure, failed launch, changed job evidence during a read and a held execution
gate. The launcher, worker bodies and manager replies are simulated. Real flock
and the production CLI/library code run; no package, manager workload, radio or
Pi job execution is implied. Local evidence is retained at
`/workspace/dxberry-update-result-tests/`.

All 978 development shell tests, repository Bash syntax/ShellCheck and the tree
check passed in Docker. Seven focused regression groups pass; all seven fail
against the previous command/library. No Go or interface source changed, so
cross-build and browser checks were not repeated.

## Pi maintenance installation

The isolated command/library patch was applied on 2026-11-23 at 12:42:23 UTC.
Its source basis is `bcc7e30f2192a24e291490d9a1e4ff1c8ae1107b`. The command
matches the tested development command; the library starts from the exact Pi
baseline and adds only the three metadata helpers, request launcher and its
adjacent schema comment. Development generation hooks are excluded.

| File under `/opt/dxberry` | Previous SHA-256 | Installed SHA-256 |
| --- | --- | --- |
| `bin/dxberry-update` | `1a96f5994acc9fd2312baa31ee8d85e090c0930dda4eb1170aeae79c5f51a2b5` | `10ee33c03d9296f108a769f9945dd0a7f58523d82db3f7d4b8e9d037ff55664b` |
| `lib/update.sh` | `c3c88ab7a7b25b13745415c038ab51accacc541fb7639de2b6437137cfb945b2` | `9e404d0c6385164dce28c6e704f45b65a10ce7ef39ee9583c4aaffc9bb77d48a` |

Root ownership and modes 0755/0644 were preserved. The installation record is
`/var/lib/dxberry/local-patches/update-result-binding.json`; originals and
checks remain at `/root/dxb-update-result-bcc7e30`, and verified staging remains
at `/root/dxb-update-result-stage`. Older patch records/backups remain retained;
this record supersedes their installed hashes for these two files.

All 889 isolated maintenance shell tests, focused binding checks, Bash syntax,
ShellCheck, tree checks and actual atomic-copy/rollback fixtures passed in
Docker. The archive was 19,340 bytes with SHA-256
`d2282b8448313f2b8698117c720d59f5d35e39eebbc21d98257d656f37d8edce`.
All five upload pieces, the complete archive and staged source bytes/modes were
verified. The additive library was installed before the command; originals
were synchronized and rollback restores the prior command before its library.

Installation acquired the existing execution/provisioning and
update/settings/record/apply locks, required no active/pending jobs, and pinned
the old source and `.4` console hashes. Independent read-only follow-up verified
both installed/backup hashes, unchanged identities of all five services,
previous maintenance records, radio/TX/helper/network files, mixer and Graywolf
configuration rows. Both PCM streams remained RUNNING; HTTP and verified HTTPS
version checks passed. The installed job-status command also returned a valid
null-job response.

No services were restarted, no update/provisioning job run and no radio command
issued. The generation helper and epoch directory remain absent. These are
reversible file and operational health checks; real systemd/package workload,
RF and audio-quality acceptance remain separate.
