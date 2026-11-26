# Actual idle WSJT-X application

This fixture adds a running application check alongside the independent Qt byte
fixtures. Its complete CI result supplies execution evidence; successful source
compilation alone establishes no application compatibility.

The Docker image stages the official checksum-pinned WSJT-X 3.0.2 amd64 package
with the existing downloader and installs it only inside its disposable root.
It compiles a tagged Go driver offline with Go 1.24.4. The runtime container is
unprivileged (UID/GID 65534, zero capabilities), read-only, network-disabled and
has no sound, USB, GPIO, serial, host display or service-manager devices/sockets.

The driver creates a private PulseAudio null sink and source plus an offscreen
Qt display. It verifies the null sink/source through the private Pulse server
before Qt enumerates devices, then requires Qt to select those fixture devices.
All HOME/XDG paths and profiles are fresh temporary data, with
N0CALL/AA00aa, rig None, automatic sequencing off and UDP requests disabled.
It starts two actual `--rig-name` instances. One passive IPv4 loopback socket
feeds real datagrams directly to the unchanged production parser; a separate
actual profile feeds the unchanged production observer. Neither receiver sends
protocol messages or invokes application controls.

Success requires actual Heartbeat and Status with the expected rig identities,
application-reported 3.0.2, three heartbeat arrivals with bounded cadence, both
synthetic call/grid identities, non-transmitting status, separate destinations
and retained N1MM enable/server/port keys, using nondefault logging sentinels
to distinguish preservation from a reset. The Qt version reference is accepted only if
both actual application processes map the same QtCore library. The final JSON
report is explicitly partial. No success report is printed on failure; process
diagnostics include bounded text logs only from new synthetic profiles. The fixture terminates its
own processes and does not claim graceful application shutdown acceptance.
An exited child is reported separately from a mismatched library, with its wait
status retained in failure diagnostics. CI runs three consecutive fresh fixtures;
every run must pass, without retrying a failed result. If the idle check fails,
the hosted runner looks for a WSJT-X segfault in its kernel journal during the
check. It records only the module name when available. The journal can lack a
matching entry, and a module name alone does not identify the cause.

After building the image separately, run on a Linux/amd64 Docker host:

```sh
docker build -t dxberry-wsjtx-application -f tests/integration/wsjtx-application/Dockerfile .
docker run --rm --network none --cap-drop ALL --read-only \
  --security-opt=no-new-privileges --pids-limit=256 --memory=2g \
  --user 65534:65534 --shm-size=128m \
  --tmpfs /tmp:rw,exec,nosuid,nodev,size=512m,mode=1777 dxberry-wsjtx-application
```

The gate still needs a reproducible non-transmitting GUI change and its Status
cadence, independent application Decode/Clear input, profile restoration and
replacement generations, real public systemd lifecycle, ARM64 capture workload
and physical radio/RF evidence. No native collection is enabled on the Pi.

## Intermittent application crash diagnostics

Post-merge run 142 failed its first of three fresh cases: the actual official
WSJT-X process was a zombie and its wait status reported SIGSEGV with a core
flag. The later two cases passed, and the job correctly remained failed. This
identifies that occurrence as actual application death; it does not establish
the cause of the older run 114 Qt-map failure or fix either finding.

Explicit `DXB_WSJTX_DEBUGGER=1` starts each real application as GDB's child.
Kernel parent/child, executable and exact argument metadata identify the actual
inferior for the original protocol/library checks. A pidfd makes cleanup safe
if GDB has already reaped that task. ASLR remains enabled; no host ptrace policy,
capability, privileged container, sound device or service socket is added. GDB
reports the faulting stack first and then bounded per-thread stacks. A tracing
stop cannot count as a running application; fault diagnostics get up to five
seconds to finish before cleanup. Only the newly generated synthetic processes
are debugged, with filtered fixture environment and bounded output. No core,
recording, station profile or credential is exported.

The diagnostic job runs only on the named crash-diagnostics branch. It executes
20 independent fresh containers, stopping on the first failure and retaining its
actual debugger output. Each uses private IPC, three GiB memory and a 512-task
limit to allow debugger overhead; the ordinary untraced gate retains its original
limits and all three cases must pass. Each debug container runs exactly one case,
so its process and IPC namespaces retire any debugger descendants on exit.

Passing diagnostic samples mean only that no failure was observed under the
debugger. Reports explicitly label that mode and leave untraced reliability and
the intermittent crash's root cause open. The normal job neither retries failed
cases nor accepts a debugger-only pass as a fix. Reproduce a faulting stack before
choosing a behavior change; do not relax actual application or Qt identity checks.
