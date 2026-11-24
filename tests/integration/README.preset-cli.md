# Preset configuration CLI check

Build `station/cmd/dxberry-preset` inside the development Docker build container
with `CGO_ENABLED=0`. The standalone command uses only the standard library;
its native Linux binary can run in a cached Python 3 Docker image.

Run `test-preset-cli.py` in a disposable off-station container with networking
disabled, a read-only root filesystem and temporary `/tmp`. Mount the compiled
binary at `/fixture/dxberry-preset`, this test script read-only and a fresh
writable evidence directory at `/evidence`. The driver must be root and able
to drop its child to UID/GID 65534; with all capabilities dropped it needs
SETUID, SETGID and DAC_OVERRIDE for the mounted evidence directory. Keep
no-new-privileges enabled. Do not mount station state, a service manager,
Docker socket, sound, serial, GPIO or USB devices.

The test refuses a non-Docker/nonroot driver, external network interfaces,
radio devices and an existing report destination. Its command is:

```sh
python3 /fixture/test-preset-cli.py
```

`--binary` and `--output` select different fixture binary/report paths if needed.
It runs the actual executable as root and as an unprivileged child, verifies
list/create/update/delete and conflict results, private file permissions,
unsupported control/path refusal and unchanged unrelated fixture state. It
also leaves a real OS stdin pipe open to verify that the command exits at its
input deadline. A closeable in-memory pipe alone did not establish that behavior.

Only after all checks pass does it create `cli-integration.json`, without
replacing earlier evidence. This is a configuration software check, with
synthetic definitions and temporary directories. It neither tunes a radio nor
establishes radio preview/recall, physical RF, Pi execution or deployment.

## HTTP and configuration runner

`test-preset-http.sh NATIVE_PRESET_BINARY EMPTY_EVIDENCE_DIRECTORY` compiles the
opt-in `presetintegration` Go fixture inside Docker and runs it against the
provided helper executable. Set `DXB_PRESET_GOPATH` to a populated Go dependency
cache. Both test containers disable networking; dependencies and Docker images
must be prepared beforehand. Default images are `golang:1.24.4-bookworm` and
`debian:trixie-slim`; `DXB_PRESET_GO_IMAGE` and `DXB_PRESET_RUN_IMAGE` can select
cached local equivalents. The runtime has a read-only root, no capabilities,
temporary `/tmp` and an empty private tmpfs at the helper's station-state path.
No station state or service-manager/hardware/Docker sockets are mounted.

The fixture requires its explicit runtime flag, Docker root, loopback-only
network interfaces, no radio devices and an empty root-owned private tmpfs.
It uses real loopback HTTP, production session/same-origin middleware and the
CLI runner to execute the actual helper for list/create/conflict/update/delete.
Authentication itself is synthetic; this is not a PAM/account-login check.
It verifies refusal before process execution, no stdin/state-path logging,
unchanged unrelated state and zero full-report refresh requests under the
production reporting policy. It never connects to rigctld or tests RF.

Logs and a successful `result.json` identify the helper digest. Existing evidence
directories with contents are refused. Missing prerequisites or failed checks
exit nonzero, rather than skip. CI/release binary jobs prepare the two Docker
images separately, then use this check with the helper built by `station/build.sh`.
That workflow is configured locally; an unrun hosted job is not a hosted CI pass.

## Read-only preview service

`test-preset-preview.sh EMPTY_EVIDENCE_DIRECTORY` uses the same populated
`DXB_PRESET_GOPATH` and image overrides as the HTTP driver above. It compiles
`internal/presetpreview` and the preset HTTP tests in `internal/httpapi`, then
runs both test executables as Docker root, with no
network except isolated loopback, a read-only root, no capabilities and a
private temporary filesystem. Integration mode refuses non-root execution,
non-loopback interfaces and radio devices. No station state or hardware socket
is mounted. Evidence directories with contents are refused and preserved.

The actual definition store, registration reader, owner claims, rig client,
production HTTP middleware and preview adapter participate. Authentication,
clock, effective-target verifier and radio endpoint are synthetic; they establish
software behavior, not PAM, real process/radio qualification, RF or Pi acceptance.
The daemon registers the preview routes with no backend until a production
verifier is supplied. CI/release binary jobs invoke
this driver so root-only fixtures are not skipped by the host user's Go tests.
