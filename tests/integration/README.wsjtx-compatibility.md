# Running-program WSJT-X compatibility gate

The native observer and UI have passed isolated parser, Qt serialization,
loopback, controller and browser fixtures. Those results do not establish that
the installed WSJT-X application and its real lifecycle behave as expected.
This gate remains open before native collection is enabled on the maintained Pi.

## Verified package staging

Use the official Linux x86_64 WSJT-X **3.0.2** package, matching the maintained
Pi's application version. The upstream
[release metadata](https://api.github.com/repos/WSJTX/wsjtx/releases/tags/v3.0.2)
was inspected on 2026-11-18:

| Field | Pinned value |
| --- | --- |
| Asset | `wsjtx-3.0.2-linux-x86_64.deb` |
| Asset ID | `452364146` |
| Size | `46499354` bytes |
| SHA-256 | `a0a00ebb74f2159b45930be1b2694b22f1a83a802622cd1d2edacb7c11e80e15` |

Run staging inside a Docker container that has Bash, coreutils, curl and CA
certificates, with this repository mounted read-only and an empty writable
directory mounted at `/evidence`:

```sh
bash tests/integration/download-wsjtx.sh /evidence
```

An already supplied package can be checked offline in the same container:

```sh
bash tests/integration/download-wsjtx.sh --verify-only /evidence/wsjtx-3.0.2-linux-x86_64.deb
```

The script accepts only the pinned asset, checks size and checksum, stages
through a temporary file and publishes without replacing an existing file.
Existing mismatched files are refused and left intact. It never extracts,
installs or executes the package. Interrupted/failed downloads remove their
temporary file. The checksum is a pin to the inspected upstream release, not
an independent signature or a claim that every future upstream replacement is
acceptable. Changing version requires a fresh source/metadata review.

## Required application acceptance

Prepare the official package and dependencies in a disposable x86_64 image.
Run the application checks with networking disabled, an unprivileged user,
temporary HOME/XDG directories, a container-local display and dummy audio.
Pass no sound, serial, GPIO, USB or host service-manager devices/sockets through.
Use only synthetic calls, grids and fixture settings. Do not copy live profiles,
contacts, credentials or audio into the fixture. Package installation belongs
inside that disposable image, never on the host or maintained Pi.

The actual running-program evidence must cover these points:

1. Record the package digest, application-reported version and Qt runtime. Confirm
   `--rig-name=RADIO` selects the expected separate INI file and protocol ID.
2. With incoming UDP requests disabled, observe unsolicited Heartbeat and Status
   using the production receiver/parser on an IPv4 loopback endpoint. The
   observer must send no messages. Record the actual schema and heartbeat
   interval; do not manufacture a Status report from a heartbeat.
3. Exercise a non-transmitting GUI change to verify Status field interpretation
   and its on-change cadence. Decode and Clear need independently reproducible
   application input; byte fixtures alone do not satisfy that acceptance point.
4. Check two independent temporary rig names, destination/profile coexistence
   and restoration. Confirm N1MM logging remains separately configured. A stopped
   instance's packets must not enter a replacement generation.
5. Separately exercise the real systemd unit/process guard, start/stop ordering,
   refusal/recovery paths and update/rollback behavior in an appropriate isolated
   systemd fixture. A normal Docker PID 1 or a mocked systemctl is insufficient
   to close that part of the gate. Do not mount the host system bus.

Record each point as passed, failed or untested with its evidence. A successful
launch or package hash does not close the entire gate. This preparation adds
neither an application acceptance pass nor a new automatic CI claim.

## Current result

The cached local environment has not run the official application. The separate
[`wsjtx-application` CI fixture](wsjtx-application/README.md) now stages the pinned
package and implements passive idle-profile checks with dummy audio; its complete
execution result is required before claiming those checks pass. The remaining
GUI, Decode/Clear, lifecycle and workload acceptance points stay open.
Offline staging checks passed in Docker: invalid
arguments, non-regular files, incorrect size, a real SHA-256 mismatch at the
expected size, interrupted and corrupt transfers, temporary-file cleanup and
preserving an existing mismatched package without attempting a download.
ShellCheck and Bash syntax checks passed. These are negative-path checks,
not evidence of a successful upstream download. The Pi's native collection remains disabled.
CPU/capture workload acceptance follows application compatibility; the existing
capture fault remains open and sparse rural APRS activity is not a failure signal.
