# Running-program Pat compatibility gate

The native client, guarded reader and default-off HTTP route have software
checks. They still require evidence from the actual Pat application and its
managed lifecycle before activation on the maintained Pi.

## Pinned application package

Use the official [Pat v1.0.1 release](https://github.com/la5nta/pat/releases/tag/v1.0.1),
matching the maintained Pi's application version:

| Field | Pinned value |
| --- | --- |
| Asset | `pat_1.0.1_linux_amd64.deb` |
| Asset ID | `697981569` |
| Size | `4940336` bytes |
| SHA-256 | `21b5c7cbf96375d10da977a014c83d5bd11b2119d72b303db75b4ff168e163c0` |

This checksum pins the inspected release asset; it is not an independent
signature. The runner refuses missing, non-regular, symlinked, wrong-size and
wrong-checksum inputs. It extracts a private copy of the verified bytes without
installing the package or executing package-maintainer scripts.

`download-pat.sh DIRECTORY` stages this exact package using the pinned size and
digest, preserves existing files and removes failed partial downloads. Its
`--verify-only PACKAGE` mode checks a supplied archive offline. The `pat-application`
CI job prepares the downloader/runtime image and pinned Go image, stages the
official package, then runs the application with networking disabled.

## Isolated application check

On a Linux/AMD64 Docker host, prepare cached Go 1.24.4 and Debian runtime images
separately. The runtime must provide `/usr/bin/dpkg-deb` and any shared libraries
required by the official executable. The default image names are
`golang:1.24.4-bookworm` and `debian:trixie-slim`; their availability alone does
not prove that the application can run. Missing runtime dependencies fail the
check. Image overrides are available for prebuilt local equivalents.

Run from this repository with a supplied package and a fresh evidence directory:

```sh
DXB_PAT_GO_IMAGE=golang:1.24.4-bookworm \
DXB_PAT_RUN_IMAGE=debian:trixie-slim \
bash tests/integration/test-pat-application.sh \
  /absolute/path/pat_1.0.1_linux_amd64.deb /absolute/path/new-evidence
```

The script orchestrates Docker; compilation and application execution both
happen inside containers. It refuses to pull images. Both containers have
networking disabled. The runtime has a read-only root filesystem, bounded
temporary storage, process/memory limits and no sound, USB, GPIO, serial,
host service-manager or Docker sockets. Only its test executable, package and
evidence destination are mounted. Do not add hardware, host networking or host
configuration mounts to make a failed check pass.

The test driver uses root only to extract files and create the private fixture
identity; Pat itself runs as UID/GID 65534 with no effective capabilities.
HOME, XDG directories, mailbox, forms, prehooks, logs and settings are temporary.
Settings use a synthetic call/grid, empty credentials, no scheduled connections
or listeners, empty modem/backend addresses and disabled version reporting.
No station profile, mail, credentials or audio is copied into the fixture.

The tagged `TestPatApplicationCompatibility` check requires the actual pinned
package. It never skips missing prerequisites or substitutes a fake HTTP server.
It verifies the application-reported version, reads three actual idle responses
through the production protocol client, stops its own fixture process with TERM
and confirms that subsequent status is unavailable. Pat v1.0.1's Unix source
registers interrupt and hangup handlers, not TERM; process termination here
does **not** establish graceful active-session shutdown or message persistence.

Only after all these checks succeed does the runner publish
`pat-application.json`, without replacing existing evidence. Even that report
has `result: "partial"` and explicitly lists untested acceptance points.
Failure exits nonzero and creates no success report. Use a fresh destination
for every run and retain the command output alongside its report.
After success, a separate capability-free container makes only the newly
published synthetic report readable by the calling account/artifact uploader.

## Remaining acceptance

The application protocol check does not exercise the public managed reader's
ownership checks against a real systemd manager. A separate isolated fixture
must establish actual unit/process/socket identity, start/stop ordering,
replacement and refusal/recovery behavior. Do not mount the host system bus;
mocked `systemctl` output does not close that gate. The separate
[`pat-manager` guest](pat-manager/README.md) now implements that isolated check;
its complete job result supplies execution evidence. It does not exercise
DXBerry's full public session hooks or physical-radio eligibility.

Actual dialing/connected transitions, active Winlink shutdown and message
persistence, station CPU/capture workload and physical radio/RF behavior remain
separate acceptance items. No real connections or RF transmissions are part of
this idle-only runner. Native Pat collection stays disabled on the maintained
Pi until the applicable gates are satisfied.

## Current evidence

On November 20, tagged Go vet, race-enabled invalid-input/evidence-preservation
checks, test-binary compilation, Bash syntax and ShellCheck passed in Docker.
The complete isolated runner rejected an invalid package with a nonzero exit
and produced no application report. These results establish failure behavior,
not real application compatibility.

On November 24, the
[actual-application job in CI run 84](https://github.com/Dustpan95/DXBerry-Pi/actions/runs/45361576931/job/138332715392)
successfully staged the pinned package and passed `TestPatApplicationCompatibility`.
The real application ran unprivileged, supplied three actual idle status reads
and became unavailable after TERM. Its explicitly partial report and runner
log are retained as `pat-application-evidence` (artifact `14903839825`, ZIP
SHA-256 `f43a102ce4b3f3235533f1d68b78a149ec6508b052ca3945439809404f890c3a`).
The job also prints the synthetic report for review without requiring an
artifact download.

The local cloud workspace's ordinary download remains unavailable; the CI
check required no Pi proxy or station application execution. Earlier
negative-path evidence is retained at `/workspace/dxberry-pat-application-tests/`.
Real systemd, connected-session and station-workload acceptance remain open.
Native collection stays disabled.

The tagged driver now owns its bounded, synchronized diagnostic capture. This
repairs its stale call to a formatter removed from production evidence types;
those production types retain their private serialization behavior. Compiling
the actual tagged driver and running its package-refusal/evidence-preservation
regressions are part of the application job, before starting the application.
