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

The idle phase uses a synthetic N0CALL/AA00aa profile with empty credentials,
empty listener and schedule lists, and version reporting disabled. The fixture starts exactly `pat http --addr 127.0.0.1:8180` as
`dxbapps`, with its private HOME/XDG settings. It reads idle status, replaces
the actual unit between metadata probes, atomically replaces the assignment
with identical bytes, refuses multiple owners, recovers the sole owner and
refuses status after stopping the unit. Production metadata and read deadlines
remain unchanged. The test never serializes private identity evidence.

The outbox check creates one synthetic P2P-only message through the Pat web
API. After a normal manager restart the API must return the same ID, subject,
body and P2P-only flag. A second synthetic profile enables a single loopback
telnet listener with fixture credentials. While a local peer holds an incoming
connection, the production reader must report the listener and connected state.
Stopping the unit must produce a successful systemd result, close the peer
socket and make status unavailable. A fresh start must report an idle listener
and recover the queued message addressed to a different synthetic callsign.
The actual guest passed these checks. [Guest log](https://github.com/Dustpan95/DXBerry-Pi/actions/runs/45896391337/job/140171226466).

The addressed peer then connects to the same loopback listener and receives
the message using the wl2k-go v1.0.1 B2F implementation used by Pat 1.0.1.
The peer runs unprivileged with a fresh temporary inbox. It checks the MID,
sender, recipient, subject and body, then reconnects to verify an empty
exchange. The managed Pat API must show an empty outbox and exactly one sent
entry with that MID. Dependencies for this guest helper are pinned in its own
Go module. The station binaries do not include it.

For the outgoing connection the test submits an explicit guest-loopback address
to the normal Pat connect endpoint. The peer waits at the transport login and
B2F handshake boundaries while the managed reader checks dialing and connected
status. An empty exchange must finish successfully, restore idle status, and
preserve the earlier sent message.

A second radio record exercises the shipped systemd template and public Pat
launcher. An absent USB function leaves the unit waiting. A synthetic sysfs
class link then lets the normal start command run first fill, wiring and the
headless session. The reader must identify Pat as a child of the launcher.
Removing the class link before stopping the unit exercises waiting and cleanup.
The fixture uses Pat alone with an empty transport list and PTT disabled.

After preparing the image, run from the repository on Linux/amd64:

```sh
docker build -t dxberry-pat-manager -f tests/integration/pat-manager/Dockerfile .
mkdir /absolute/path/new-evidence
docker run --rm --network none --cap-drop ALL --read-only \
  --security-opt=no-new-privileges --pids-limit=128 --memory=3g \
  --user "$(id -u):$(id -g)" --tmpfs /tmp:rw,nosuid,nodev,size=64m \
  -v /absolute/path/new-evidence:/evidence dxberry-pat-manager
```

Success requires both tagged Go tests to pass and exactly one positive
guest completion marker, with no failure marker. The console log contains an
explicitly partial synthetic report. Failure to obtain assets, boot the guest,
start Pat or verify ownership fails the job; it is never converted to a skip.

ARDOP, physical eligibility, ARM64 capture workload and RF acceptance remain open. Native collection stays disabled on the maintained Pi.
