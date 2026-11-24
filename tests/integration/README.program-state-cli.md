# Program generation helper process check

`test-program-state-cli.sh NATIVE_HELPER EMPTY_EVIDENCE_DIRECTORY` compiles the
explicitly tagged fixture and runs the actual helper in Docker. Set
`DXB_PROGRAMSTATE_GOPATH` to an existing dependency cache. The driver uses cached
`golang:1.24.4-bookworm` and `dxberry-program-state-runtime:fixture` images by
default; build the runtime with the existing `tests/integration/Dockerfile`
(Bash, jq and core tools on Debian). Trusted local image overrides are
`DXB_PROGRAMSTATE_GO_IMAGE` and `DXB_PROGRAMSTATE_RUN_IMAGE`. Image acquisition
is separate from the network-disabled checks.

The driver refuses linked/missing/non-executable helpers and existing evidence.
It does not invoke the helper on the host. Each runtime container has a read-only
root, no capabilities, no network, no-new-privileges, limited memory/PIDs and
private tmpfs state and runtime lock directory. Only the compiled fixture,
native helper and readable copies of the public unit/common/record sources are
mounted, along with a small response-loss wrapper used only by the fixture.
Do not mount station state, Docker socket, service manager or radio devices into
the runtime containers. The fixture checks those boundaries and refuses to run
against a nonempty state directory or a live machine.

The root run checks exact argument refusal, fixed private responses, state
permissions, generation rotation, ignored/open OS stdin, real process-shared
lock timeout and recovery, damaged-state refusal and unrelated fixture state.
A closed OS stdout pipe must return uncertainty after publication without
SIGPIPE or rollback. A filled output pipe must be bounded by the actual helper's
watchdog; its main process must exit with uncertainty without waiting for a
reader. These checks cannot be established by an in-memory response writer.

A separate UID 65534 run checks real privilege refusal before state access.
An activation run reads the template, requires the non-ignored generation
condition before presence/preparation, and executes that exact helper command.
It verifies success while the parent's real record lock is held, store-lock
refusal/recovery and damaged-state refusal. Stop/main command declarations are
checked, but the fixture does not simulate a systemd manager or claim that it
executed preparation, the program or cleanup.
A record run sources the real Bash common/record libraries and calls only
`dxb_radio_save`, with the actual helper. It verifies owner change-and-return,
response loss after advance, failed record publication, invalid/corrupt state
and unchanged cached/unrelated data. Its synthetic pinned selector is metadata
in tmpfs; no scan/apply, program or radio action runs. The record parent is
disposable tmpfs and generation state has its own private nested tmpfs.
The driver requires explicit passing test names in both logs, preventing an
omitted tag or skipped fixture from becoming a successful report. `result.json`
binds success to the helper's SHA-256. Failed logs and existing evidence are
retained rather than overwritten by rerunning the driver.

This is filesystem/process boundary and unit-prefix verification. It does not
start/stop systemd units, prove complete hook coverage or program-free authority,
control a radio or establish
physical acceptance. ARM64 must run on a native ARM64 runner for this check;
cross-compilation alone is not execution. CI/release jobs are configured to run
it against their built native helper; a local workflow parse is not hosted CI.
