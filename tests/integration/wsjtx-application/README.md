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
Qt display. All HOME/XDG paths and profiles are fresh temporary data, with
N0CALL/AA00aa, rig None, automatic sequencing off and UDP requests disabled.
It starts two actual `--rig-name` instances. One passive IPv4 loopback socket
feeds real datagrams directly to the unchanged production parser; a separate
actual profile feeds the unchanged production observer. Neither receiver sends
protocol messages or invokes application controls.

Success requires actual Heartbeat and Status with the expected rig identities,
application-reported 3.0.2, three heartbeat arrivals with bounded cadence, both
synthetic call/grid identities, non-transmitting status, separate destinations
and retained N1MM configuration. The Qt version reference is accepted only if
both actual application processes map the same QtCore library. The final JSON
report is explicitly partial. No success report is printed on failure; process
diagnostics contain only new synthetic profiles. The fixture terminates its
own processes and does not claim graceful application shutdown acceptance.

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
