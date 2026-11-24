# Radio event manager acceptance prerequisites

The experimental queue remains off. Shell fixtures and offline unit parsing do
not establish real systemd delivery, start limiting, cancellation, retirement
or recovery. Run development checks in Docker; do not use the working Pi as a
test fallback.

`probe-radio-event-manager.sh` is read-only. It checks root/tool availability,
PID 1, the unified cgroup mount's writable view and a bounded system-manager
version query. Exit 77 means unavailable, not a test pass. Exit 0 means only
those prerequisites were observed: it does not prove actual cgroup delegation,
private isolation, service behavior or permission to activate the queue.

Before behavioral acceptance, provide an explicitly isolated Docker test
environment with its own system manager and writable private cgroups. Never
bind the host's writable cgroup hierarchy or system bus into a test container.
A fully emulated guest inside Docker is a possible alternative when trusted
QEMU/kernel/root-filesystem assets are available; it is not established here.
No privileged container or host cgroup modification is needed to report the
current blocker.

The behavioral run must use the production helper/common library and final
service/path/timer templates, with isolated runtime/unit names and an inert
apply fixture. Preserve production semantics while replacing radio operations.
It must establish arrival coalescing, arrivals during apply, failed work and
retry, actual start-limit recovery, timer/path reactivation, boot ordering,
stop/cancellation cleanup and surviving-child behavior. Consumer retirement,
release-tree swaps, retained-work migration and failure recovery require their
own positive and negative evidence. Fixtures remain fixtures for hardware.

## Current environment evidence

The cached image contains systemd 257.13 at `/lib/systemd/systemd`; `systemd`
is not on PATH. An initial bare-command probe therefore returned 127. Direct
execution returned 1 with the explicit not-booted-with-systemd diagnostic.

A later private tmpfs bootstrap and small private D-Bus configuration produced
a successful `ListNames` query. Console logging then showed systemd entering
user mode, locating unified cgroups and failing to create `/init.scope` because
the cgroup view is read-only. Manager allocation returned 1. The separate
D-Bus descriptor-limit warning remained, but it did not prevent that bus query
or explain the final manager allocation failure. A writable private cgroup
environment is still required; this probe did not establish a system manager.

All manager probes used a read-only container root, zero capabilities and no
external network. The boot marker and bus lived only in private tmpfs; they
were bootstrap diagnostics, not evidence of a correctly booted system manager.
No host cgroup write, privileged container or Pi test was substituted.

QEMU was not found in the tested image or workspace, and no usable guest kernel
was identified in the workspace or `/boot`. The similarly named Go
PE-parser test fixture is only 474 bytes and is not a boot kernel. An ordinary,
certificate-verified request for official Debian metadata failed with HTTP 503,
so guest dependencies could not be obtained through that route. No alternate
route or Pi downloader/proxy was used. Evidence is retained at
`/workspace/dxberry-manager-tests/`.

The read-only prerequisite script passed Bash syntax and ShellCheck, then
returned 77 with three observed omissions: PID 1 is not systemd, cgroup2 is
read-only and the system manager does not return a version. This is an expected
unavailable result, not manager acceptance. Application source did not change;
the already recorded full software suites were not repeated for this probe.

## GitHub Actions guest

The `systemd-queue` CI job builds `systemd/Dockerfile` and boots a complete
Debian guest with its distribution kernel using QEMU software emulation inside
Docker. The outer container runs without capabilities, network, host devices,
host cgroup mounts or a host system bus. Its root filesystem is read-only and
its only writable bind mount holds test evidence. Guest device nodes are encoded
in the initramfs archive; building it does not create host device nodes.

The guest supplies its own PID 1/systemd manager and private cgroups. It runs
copies of the production helper/common library and service/path/timer templates,
with isolated unit/runtime names and an inert apply command. Tests cover boot
ordering, coalescing, arrivals during apply, actual start limiting, independent
timer recovery without resetting the service, explicit path reactivation,
stop/retry and surviving-child cleanup. Source checksums are verified afterward.
A success marker is emitted only after the assertions pass; a skipped manager
probe or a QEMU timeout cannot satisfy the job. Serial logs are retained even
when the job fails.

This job is being introduced for remote execution; its source/syntax checks do
not establish a successful guest run. Actual workflow results provide that
separate evidence. It does not claim hardware or real udev-device delivery,
consumer retirement, release-tree replacement or rollback recovery acceptance.
The queue stays off on the maintained Pi.

The first hosted guest booted successfully and passed coalescing and
arrivals-during-apply checks. It exposed an assertion error: this systemd keeps
the service's earlier `exit-code` result when a new start is rate-limited, while
the path reports `unit-start-limit-hit`. The rate-limit check therefore requires
that actual path result together with a failed service, before testing timer
recovery without resetting it. This initial failed run is not full acceptance.
