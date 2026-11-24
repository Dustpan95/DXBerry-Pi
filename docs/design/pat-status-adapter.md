# Read-only Pat status adapter

The local `station/internal/pat` package verifies a managed Pat instance and
reads its existing status endpoint. The development daemon has a read-only
route behind the default-off `-pat-native-status` flag. No reader or route is
created when that option is off; there is no native browser panel or background
poller. Its code is in the coordinated station bundle, with the flag off on the
maintained Pi. It
never starts Pat or changes radio/program configuration.
This independent client work does not close the WSJT-X compatibility gate.
On November 23 the maintained Pi ran the isolated
[`.4` maintenance console](program-ownership.md), which includes neither
this native Pat reader nor its HTTP option. The dated `.2` evidence below
describes the preceding maintenance baseline. The
[November 24 deployment](station-deployment-2026-11-24.md) installed the matching
software foundations while preserving the default-off activation boundary.

## Verified upstream contract

Pat v1.0.1, matching the installed application version, defines:

- [`api/api.go`](https://github.com/la5nta/pat/blob/v1.0.1/api/api.go):
  `GET /api/status` calls `GetStatus()` and encodes its result. The handler does
  not explicitly set a JSON Content-Type, so the client accepts valid JSON
  without requiring that header.
- [`api/types/types.go`](https://github.com/la5nta/pat/blob/v1.0.1/api/types/types.go):
  `active_listeners`, `connected`, `dialing`, `remote_addr`, `http_clients` and
  `config_hash` are its status fields.
- [`app/app.go`](https://github.com/la5nta/pat/blob/v1.0.1/app/app.go):
  `GetStatus()` reads connection/listener state, client addresses and a hash of
  the configuration. It does not initiate a connection or change configuration.

The existing DXBerry command starts Pat at `127.0.0.1:8180` and keeps its full
interface behind the authenticated proxy. The new client uses only
`http://127.0.0.1:8180/api/status`; it cannot take a browser-selected host, port,
path, method or payload. It follows no redirects and uses no environment proxy.
This matters even for GET: Pat's connect/disconnect handlers do not constrain
their HTTP method in the reviewed router. An alternate path is not a safe read.

The projected status contains only connection/dialing booleans, active listener
names and the local successful observation time. It excludes remote addresses,
HTTP client addresses, configuration hashes, mail, logs, prompts and account
information. Connected means a Winlink transport connection, including possible
non-RF transports. It is not a PTT reading, RF activity indicator, proof of
decoded audio, or contact delivery receipt.

## Read and failure boundaries

Each HTTP request has a one-second deadline, honoring an earlier caller
deadline, with a 500 ms dial/header timeout. Header input is limited to 16 KiB;
the response body to 64 KiB; listeners to 32 names of at most 128 bytes each.
Automatic decompression and persistent connections are disabled. It makes one
request, with no retry, redirect, retained state or background producer.

Missing/null booleans or listener arrays, duplicate fields, case aliases,
invalid UTF-8/JSON, extra JSON values, oversized input and invalid listener names
are unavailable. The reviewed `ActiveListeners()` constructs an empty array
when no listeners are active; an unknown/null value is not an idle success.
Unknown fields are allowed within the input bound and discarded. Raw response
bodies, addresses and transport errors never appear in the returned error. The
caller receives an error and no previous observation; it must not present that
as an idle or disconnected success.

## Integration still required

The existing cached program-holder checks have been
[hardened](program-ownership.md) while preparing this integration. They still
do not provide fresh systemd invocation/process identity for a native read.
The dedicated reader described below supplies local verification code. The
daemon's HTTP wiring is implemented behind its default-off flag; actual
application/systemd acceptance, native display and activation remain required.

1. Complete real application and service-manager acceptance for the dedicated
   version/session/process checks. A source review and fixture metadata are
   not evidence that the installed Pat/systemd combination behaves correctly.
2. Add the native display and any watched-only polling/bounded publication, with
   independent observation age, after compatibility acceptance. Preserve the
   full Pat window. A failed read must clear its live label rather than
   manufacturing false booleans or retaining live state. The implemented HTTP
   endpoint performs explicit reads only and adds no periodic work.
3. The actual Pat 1.0.1 initial/idle handler and TERM-unavailability check passed
   in isolated Docker with synthetic settings and no radio/serial/audio devices,
   station account or outbound network. Its partial report is linked below;
   active-session lifecycle and persistence remain untested.
4. Measure the eventual observed-client workload, including its metadata
   subprocesses, before station activation. Preserve idle reporting behavior;
   the current capture fault makes a fixture CPU result insufficient acceptance.
5. Validate the eventual complete station candidate before a reversible Pi
   deployment. Do not start Pat or register the current manual Graywolf CODEC
   incidentally to test this client. Real Winlink/RF operation remains separate.

## Managed reader

`NewReader(recordsPath)` prepares a lazy Linux reader; it opens no program or
network connection and writes no state. Only `Reader.Read(ctx, radio)` is an
exported read entry point. The fixed HTTP transport is private, preventing a
caller from accidentally skipping the identity checks.

Each explicit read requires:

- Exactly one current `pat` assignment in the trusted root-owned radio file,
  belonging to the requested radio. Duplicate JSON keys, malformed records,
  writable/untrusted files, symlinks and non-regular files are refused. Its
  content digest, inode and timestamps identify the particular file revision.
- The local, non-root `dxbapps` account and Debian package metadata reporting
  installed Pat 1.0.1. The supported installation uses root-owned `/usr/bin/pat`.
  Unknown versions or another installation layout stay unavailable.
- The exact radio's loaded, active/running systemd unit, a nonzero invocation
  ID/MainPID and its bounded cgroup-v2 process inventory. The manager is checked
  on both sides of each metadata probe.
- One process in that cgroup using the installed executable, running as
  `dxbapps`, with the managed `pat http --addr 127.0.0.1:8180` arguments.
  Its kernel process-start ticks distinguish PID reuse. Process membership,
  all UID fields and executable/file identities are checked independently.
- One IPv4 `127.0.0.1:8180` TCP listener with the expected UID and a socket inode
  held by that Pat process. Conflicting/wildcard/IPv6 listeners on that port are
  unavailable. A healthy HTTP response from an unrelated process is insufficient.

The reader repeats the verification after the HTTP request and compares the
complete private identity. A unit restart, PID reuse, listener replacement,
binary change or radio-file rewrite discards the response, including a rewrite
back to identical values. The result contains only the radio name and projected
status; invocation IDs, process IDs, paths, hashes and socket metadata remain
private. No previous observation is retained after failure. Even a successful
result describes an observation, not a promise the process remains running.

Reads serialize behind a cancellable gate. A three-second context budget covers
queueing, two one-second metadata-probe budgets and the existing one-second HTTP
budget; earlier caller deadlines win. Metadata command cleanup can wait up to
200 ms. Source reads are bounded: 2 MiB/128 radios/64 JSON levels for the radio
file, 64 KiB per metadata command or process file, 128 KiB for local account
metadata, 64 cgroup directories/256 process IDs, 1 MiB/4,096 data rows per TCP
table and 1,024 process descriptors. Missing or over-limit evidence is unavailable.
Only fixed read-only `dpkg-query` and `systemctl show` commands run, with a
sanitized environment; cancellation ends only those spawned metadata commands.
The reader never signals Pat or reads its settings, environment, mail or audio.

## Default-off HTTP boundary

The development daemon registers `GET /api/radio/{name}/pat` only when its
`-pat-native-status` option is explicitly enabled. The shipped service template
does not enable it. The flag is an implementation gate, not evidence that the
feature is ready for station activation. It must remain off on the maintained
Pi until the application/lifecycle and workload gates above are satisfied.

The route shares the normal station authentication and same-origin middleware,
checks the known radio, and invokes the dedicated fresh-identity reader. It has
no query fields or body, no destination override and no program-control action.
HEAD also performs no read. Non-GET/control attempts are refused before reader
work. Each read has a three-second context budget and honors earlier caller
cancellation. Reads do not poke full-station reporting or acquire a transmitter.

Success returns only the radio and projected observation with `Cache-Control:
no-store`. An error, cancelled/late answer, mismatched radio, missing observation
time or unknown listener list returns fixed `503 unavailable` JSON, without
private upstream details or the preceding connected/idle state. Unknown radios
are 404; unexpected status input is 400. When the feature is off the route is
absent, not a fabricated idle response. Constructor/path errors also leave it
unregistered while the rest of the console continues.

## Running-application gate

The official Pat v1.0.1 release metadata was inspected on November 20. The x86_64
fixture package is `pat_1.0.1_linux_amd64.deb`, asset ID `697981569`, 4,940,336
bytes, SHA-256
`21b5c7cbf96375d10da977a014c83d5bd11b2119d72b303db75b4ff168e163c0`.
See the [upstream release](https://github.com/la5nta/pat/releases/tag/v1.0.1).
This digest pins the inspected release asset; it is not an independent signature.

The earlier ordinary Docker download to private staging failed with HTTP 503 and removed
its partial file. The maintained Pi's ARM64 executable is present, but the cached
AMD64 Docker runtime has no usable ARM64 emulation (`exec format error` on a
version-only candidate probe). No Pat instance was started on the Pi as a
substitute. The staged
download command/log and runtime probe remain in
`/workspace/dxberry-pat-managed-tests/`.

The [isolated application runner](../../tests/integration/README.pat-compatibility.md)
now provides a reproducible idle-only check using the pinned actual package,
temporary synthetic settings and a hardware-free, network-disabled Docker
runtime. It invokes the production protocol client, checks repeated actual idle
responses, and verifies unavailable status after stopping its own fixture.
Missing or invalid packages fail rather than skip, and existing reports cannot
be overwritten. Any successful report is explicitly partial: real systemd
authority, dialing/connected transitions, active-session shutdown/persistence,
capture workload and physical RF remain untested.

On November 24, the pinned actual application passed the
[idle compatibility check in CI](https://github.com/Dustpan95/DXBerry-Pi/actions/runs/45361576931/job/138332715392):
version/architecture and package integrity, unprivileged execution, three actual
idle HTTP responses, and unavailable status after TERM. This closes the
idle-protocol application subgate. Its report remains partial and does not
establish the real managed reader, graceful active-session shutdown, persistence,
workload or physical radio gates. Native collection remains off on the Pi.

## Software evidence

The client has real loopback HTTP fixture checks for the fixed GET path, absence
of credentials, private-field projection, redirect refusal, non-200 responses,
earlier/default deadlines, slow/partial/oversized bodies and sanitized transport
failure. Parser checks cover complete idle/dialing/connected responses,
malformed/missing/null/ambiguous fields, listener limits and invalid UTF-8.
On November 20, Go vet and all six race-enabled test groups passed in Docker
with networking disabled. The package's test binary also cross-compiled for
Linux/ARM64; that binary was not executed on the Pi. Evidence is retained at
`/workspace/dxberry-pat-status-tests/` (`vet.log`, `race.log`, and
`arm64-compile.log`). No actual Pat application compatibility, station integration
or physical acceptance is claimed by these tests.
The CI-required Staticcheck pass remains outstanding: that tool is absent from
the cached build image/PATH, and dependency/tool acquisition remains blocked by
cloud connectivity. The other checks are not a substitute for that pass.

The guarded-reader tests additionally cover invalid/ambiguous assignments,
unknown package versions, stopped/stopping or changed units, wrong process UID,
zombies, changed executable/arguments, wrong listener identity, duplicate Pat
processes/listeners, bounded files and metadata-command cancellation. A real
Linux loopback socket and `/proc` check runs inside Docker. Combined real HTTP
fixtures with synthetic identity sources prove that a response is discarded on
assignment rewrites, process/listener replacement and binary modification.
These checks do not run the Pat application or a real systemd manager.

On November 20, all 15 Pat test groups and the full development tree's Go vet
and race suites passed in Docker with networking disabled. The guarded package
also cross-compiled for Linux/ARM64. Final evidence is retained in
`/workspace/dxberry-pat-managed-tests/`: `race.log`, `vet.log`, `go-vet-all.log`,
`go-all.log` and `arm64-compile.log`. The maintained console stays at
`0.5.0-rc4+idle-reporting.2`; this reader is not part of that installed binary.

The subsequent HTTP/default-option work passed seven focused regression groups
and the full development tree's Go vet/race suite in Docker. Checks use the
production station middleware with synthetic authentication/reader sources,
including real loopback HTTP requests. They cover login/same-origin rejection,
unknown radios, query/body/control/HEAD refusal before reader work, private and
previous-state suppression, caller cancellation, a missing observation and the
default-off absence of both reader construction and route registration. An
explicitly enabled reader with missing fresh records stays unavailable despite
a cached Pat assignment. These are API/software checks, not application acceptance.

The full development ARM64 console and settings helper also built, with VCS
stamping disabled for the isolated container's repository mount. The console
artifact is labeled `0.5.0-rc4+pat-status-dev`; its ELF machine is AArch64 and its
shared-library dependencies remain Opus, PAM and libc. Its SHA-256 is
`ff58a980474eb3d897af6bfa358bf7a4f0b7345e45c6f99148eb01158046886f`.
The artifacts were not installed on the Pi. Evidence is retained at
`/workspace/dxberry-pat-http-tests/`, including `focused.log`, `go-vet-all.log`,
`go-all.log`, `arm64-build.log`, `elf.txt`, `libraries.txt` and
`binaries.sha256`. Staticcheck and real application/systemd/workload acceptance
remain outstanding. The maintained Pi stays on the isolated `.2` console.

The metadata-output limiter subsequently received a subprocess regression fix.
Embedding `bytes.Buffer` promoted its `ReadFrom` method, allowing Go's subprocess
copy path to bypass the custom bounded `Write`. The buffer is now a named
field. A finite 65,537-byte subprocess first reproduced the bypass; the fixed
reader refuses it while accepting exactly 65,536 bytes. The earlier infinite
output test could finish on its deadline and therefore did not establish this
bound. This source correction does not enable the reader or establish actual
Pat application acceptance. Evidence is retained with the service work at
`/workspace/dxberry-service-binding-tests/`.

That maintenance checkpoint used the separate `0.5.0-rc4+idle-reporting.4`
console. The November 24 coordinated candidate includes this reader's code
with its activation flag off. Pi access uses DockerPC.
