# Actual Pat reader with a real manager

The `pat-manager` job builds a private Debian guest and boots its distribution
kernel and real systemd with QEMU software emulation inside Docker. The outer
container has no capabilities, network, host devices, host system bus or host
cgroup mounts. Only a fresh evidence directory is writable on the host. The
guest has loopback only. Both the test and Pat service use systemd private
device namespaces, checked to exclude sound, USB, GPIO and radio devices.

The image downloads and verifies the same official Pat 1.0.1 amd64 package as
the [idle application gate](../README.pat-compatibility.md), then installs it
in the disposable guest. Its actual package database, protected executable,
`dxbapps` account, systemd invocation, process cgroup and listener descriptors
are read by the unchanged production authority. No command output, process
tree or HTTP response is substituted. Compilation is offline inside Docker.

Only newly generated synthetic settings are used: N0CALL/AA00aa, empty
credentials, no scheduled connections or listeners and disabled version
reporting. The fixture starts exactly `pat http --addr 127.0.0.1:8180` as
`dxbapps`, with its private HOME/XDG settings. It reads idle status, replaces
the actual unit between metadata probes, atomically replaces the assignment
with identical bytes, refuses multiple owners, recovers the sole owner and
refuses status after stopping the unit. Production metadata and read deadlines
remain unchanged. The test never serializes private identity evidence.

After preparing the image, run from the repository on Linux/amd64:

```sh
docker build -t dxberry-pat-manager -f tests/integration/pat-manager/Dockerfile .
mkdir /absolute/path/new-evidence
docker run --rm --network none --cap-drop ALL --read-only \
  --security-opt=no-new-privileges --pids-limit=128 --memory=3g \
  --user "$(id -u):$(id -g)" --tmpfs /tmp:rw,nosuid,nodev,size=64m \
  -v /absolute/path/new-evidence:/evidence dxberry-pat-manager
```

Success requires the actual tagged Go test to pass and exactly one positive
guest completion marker, with no failure marker. The console log contains an
explicitly partial synthetic report. Failure to obtain assets, boot the guest,
start Pat or verify ownership fails the job; it is never converted to a skip.

This unit omits DXBerry prepare/session/stopped hooks and physical-radio
eligibility. Passing it would establish managed-reader evidence, not the whole
public session lifecycle, active Winlink shutdown/message persistence, ARM64
capture workload or RF acceptance. Collection remains off on the maintained
Pi. The gate is pending its first complete CI execution.
